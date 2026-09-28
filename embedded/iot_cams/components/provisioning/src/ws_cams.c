/* ws_cams.c — W5 real server-side sink + hello emit + viewer
 * lifecycle.
 *
 * W4 landed the URI handler skeleton: handshake accept + drop
 * the post-handshake frame stream. W5 wires the live seam:
 *
 *   - capture the httpd handle alongside the viewer fd so the
 *     static sink functions (server_sink_send_bin/_text) can
 *     dispatch via httpd_ws_send_frame_async(hd, fd, &pkt)
 *     from any context (the httpd worker for the handshake,
 *     the cam_stream FreeRTOS task for the binary frames);
 *
 *   - install the static `s_server_sink` on handshake accept
 *     so cam_stream_loop_iteration() actually pushes bytes;
 *
 *   - emit the JSON hello frame as the FIRST text frame of
 *     the session (REQ-WS-002 shape);
 *
 *   - mirror the esp32-cam-surveillance reference's
 *     heal-within-one-frame pattern: any failed async send
 *     clears the sink and disarms future frames. At 10 fps
 *     a vanished viewer is re-attachable within ≤ 1 frame
 *     period without any server-wide close-callback wiring;
 *
 *   - keep the drop path (post-handshake inbound frames are
 *     still drained and discarded — the {"cmd":"stream"}
 *     control plane is an out-of-scope follow-up).
 *
 * Module-static state:
 *   - s_httpd         — captured at handshake; NULL otherwise.
 *   - s_viewer_fd     — -1 when no viewer; set at handshake accept.
 *   - s_uri_registered — URI registration idempotency.
 *   - s_server_sink   — the sink vtable installed at handshake.
 *   - s_hello_buf / STATIC_HELLO_BUF_LEN — render the hello JSON
 *     in a static 256 B buffer, the same buffer the send path
 *     passes to httpd_ws_send_frame_async (no heap, no copy).
 *
 * IP-up re-attach and the post-disconnect cleanup live in W7
 * (this file stays compact; the re-attach hook is a single
 * static function added to W7 with no changes to the sink).
 */

#include <stddef.h>
#include <string.h>

#include "esp_event.h"
#include "esp_log.h"
#include "esp_http_server.h"
#include "esp_mac.h"
#include "esp_wifi.h"

#include "cam_stream.h"

#include "ws_cams.h"

#define TAG "ws_cams"

/* W4 path hard-coded; W8 routes through CONFIG_FIRMWARE_WS_PATH. */
#define WS_CAMS_URI_PATH "/ws/cams"

/* Hello + status builders (W3) live in cam_stream_wire.c so the
 * upstream provider can own the schema. */
#include "cam_stream.h"

/* ---------- module-static state (W4 + W5) ---------- */

static int           s_viewer_fd     = -1;
static bool          s_uri_registered = false;
static httpd_handle_t s_httpd         = NULL;

/* ---------- the server-side sink (W5) ---------- */

/* Async-send helpers; only callable from a context where a
 * failed dispatch is recoverable (the httpd worker that owns
 * the connection, or the cam_stream task via the seam). On
 * failure the sink clears state itself so the next loop
 * iteration stops trying to push to a dead fd. */

static esp_err_t server_sink_send_bin(const uint8_t *buf, size_t len)
{
    if (s_httpd == NULL || s_viewer_fd < 0 || buf == NULL || len == 0) {
        return ESP_ERR_INVALID_STATE;
    }
    httpd_ws_frame_t pkt = {
        .final   = true,
        .type    = HTTPD_WS_TYPE_BINARY,
        .payload = (uint8_t *)buf,
        .len     = len,
    };
    esp_err_t r = httpd_ws_send_frame_async(s_httpd, s_viewer_fd, &pkt);
    if (r != ESP_OK) {
        /* Mirror the reference's "viewer disconnected" path:
         * drop the sink so the loop body's next iteration falls
         * into the drop counter, and clear the captured fd so
         * the next handshake wins the viewer slot. */
        cam_stream_sink_install(NULL);
        int fd = s_viewer_fd;
        s_viewer_fd = -1;
        ESP_LOGW(TAG, "viewer disconnected fd=%d (send_bin failed: %s) \u2014 "
                      "slot freed",
                 fd, esp_err_to_name(r));
    }
    return r;
}

static esp_err_t server_sink_send_text(const char *buf, size_t len)
{
    if (s_httpd == NULL || s_viewer_fd < 0 || buf == NULL || len == 0) {
        return ESP_ERR_INVALID_STATE;
    }
    httpd_ws_frame_t pkt = {
        .final   = true,
        .type    = HTTPD_WS_TYPE_TEXT,
        .payload = (uint8_t *)buf,
        .len     = len,
    };
    esp_err_t r = httpd_ws_send_frame_async(s_httpd, s_viewer_fd, &pkt);
    if (r != ESP_OK) {
        cam_stream_sink_install(NULL);
        int fd = s_viewer_fd;
        s_viewer_fd = -1;
        ESP_LOGW(TAG, "viewer disconnected fd=%d (send_text failed: %s) "
                      "\u2014 slot freed",
                 fd, esp_err_to_name(r));
    }
    return r;
}

static bool server_sink_is_connected(void)
{
    if (s_httpd == NULL || s_viewer_fd < 0) {
        return false;
    }
    return httpd_ws_get_fd_info(s_httpd, s_viewer_fd)
           == HTTPD_WS_CLIENT_WEBSOCKET;
}

static const cam_stream_sink_t s_server_sink = {
    .send_bin     = server_sink_send_bin,
    .send_text    = server_sink_send_text,
    .is_connected = server_sink_is_connected,
};

/* ---------- helpers ---------- */

/* Convert 6-byte MAC to 12-char lowercase hex (no separators);
 * mirrors sta_server.c:mac_to_hex_lower so the same canonical
 * MAC surfaces on /whoami and /ws/cams hello frames. */
static esp_err_t mac_to_hex_lower(const uint8_t mac[6], char *out, size_t out_len)
{
    if (mac == NULL || out == NULL || out_len < 13) {
        return ESP_ERR_INVALID_ARG;
    }
    for (int i = 0; i < 6; i++) {
        uint8_t b = mac[i];
        uint8_t hi = (b >> 4) & 0x0F;
        out[i * 2]     = (hi < 10) ? ('0' + hi) : ('a' + hi - 10);
        uint8_t lo = b & 0x0F;
        out[i * 2 + 1] = (lo < 10) ? ('0' + lo) : ('a' + lo - 10);
    }
    out[12] = '\0';
    return ESP_OK;
}

/* Build the hello JSON into `out` using cam_stream's W3
 * builder. Returns the rendered length or 0 on overflow. */
static size_t build_hello(char *out, size_t out_len)
{
    cam_stream_identity_t id = {0};

    uint8_t mac[6] = {0};
    esp_err_t r = esp_read_mac(mac, ESP_MAC_WIFI_STA);
    if (r != ESP_OK) {
        ESP_LOGW(TAG, "hello: esp_read_mac failed: %s", esp_err_to_name(r));
    } else {
        (void)mac_to_hex_lower(mac, id.mac, sizeof(id.mac));
    }

    /* Kconfig-defined device name; same string the softAP SSID
     * and the /whoami handler surface. Empty allowed. */
    const char *name = CONFIG_PROVISIONING_DEVICE_NAME;
    if (name != NULL) {
        strncpy(id.name, name, sizeof(id.name) - 1);
    }
    /* Firmware version — same value app_main publishes via
     * provisioning_app_info_t.fw_version; for the WS hello
     * we mirror by reading IDF version (which is the closest
     * published chip-firmware identifier the operator sees
     * on the STA /whoami route). */
    strncpy(id.fw, esp_get_idf_version(), sizeof(id.fw) - 1);

    return cam_stream_wire_build_hello(&id, out, out_len);
}

/* ---------- handshake ---------- */

/* Reject body for a second concurrent viewer. Kept tiny and
 * greppable; the server closes the second-handshake socket
 * right after dispatching this frame. */
static const char VIEWER_LIMIT_JSON[] =
    "{\"type\":\"error\",\"reason\":\"viewer_limit\"}";

/* W6 — single-viewer enforcement. If a different viewer slot
 * is already taken, reject the new handshake with a short
 * text error frame and ESP_FAIL (the httpd closes THAT
 * socket). The active viewer's session is untouched. */
static esp_err_t viewer_reject(int new_fd)
{
    ESP_LOGW(TAG,
             "second WS handshake fd=%d rejected "
             "(single-viewer policy; active fd=%d)",
             new_fd, s_viewer_fd);
    httpd_ws_frame_t pkt = {
        .final   = true,
        .type    = HTTPD_WS_TYPE_TEXT,
        .payload = (uint8_t *)VIEWER_LIMIT_JSON,
        .len     = sizeof(VIEWER_LIMIT_JSON) - 1,
    };
    (void)httpd_ws_send_frame_async(s_httpd, new_fd, &pkt);
    return ESP_FAIL;
}

/* Decide whether the handshake is a single-viewer reject or a
 * proceed-to-accept. Returns ESP_OK to proceed, ESP_FAIL to
 * reject (and close the new socket). */
static esp_err_t ws_cams_check_single_viewer(httpd_req_t *req,
                                              int new_fd)
{
    /* No active viewer slot? Accept. */
    if (s_viewer_fd < 0 || s_httpd == NULL) {
        return ESP_OK;
    }

    /* Active slot's fd is dead (e.g. previous client crashed
     * without a graceful close). Treat the slot as free so
     * the new handshake can win it. */
    if (httpd_ws_get_fd_info(s_httpd, s_viewer_fd) !=
            HTTPD_WS_CLIENT_WEBSOCKET) {
        ESP_LOGW(TAG,
                 "active viewer slot fd=%d is dead — recycling "
                 "for the new handshake",
                 s_viewer_fd);
        cam_stream_sink_install(NULL);
        s_viewer_fd = -1;
        return ESP_OK;
    }

    /* Same fd reconnecting (httpd may re-handshake the same
     * socket) — accept; not a second viewer. */
    if (new_fd == s_viewer_fd) {
        return ESP_OK;
    }

    /* Active viewer is alive on a different fd — reject. */
    return viewer_reject(new_fd);
}

static esp_err_t viewer_accept(httpd_req_t *req)
{
    int fd = httpd_req_to_sockfd(req);
    if (fd < 0) {
        ESP_LOGE(TAG, "viewer_accept: httpd_req_to_sockfd failed");
        return ESP_FAIL;
    }

    /* W6 — single-viewer enforcement. May reject with
     * viewer_limit text + ESP_FAIL (closes the new socket). */
    esp_err_t chk = ws_cams_check_single_viewer(req, fd);
    if (chk != ESP_OK) {
        return chk;
    }

    s_httpd     = req->handle;
    s_viewer_fd = fd;

    /* Install the live sink BEFORE the hello emit so the text
     * path itself funnels through the same TX-mutex that the
     * stream task uses (and so any failure to dispatch the
     * hello routes through the failure path that clears the
     * slot). */
    cam_stream_sink_install(&s_server_sink);

    /* First text frame of the session, per REQ-WS-002. */
    char hello[256];
    size_t n = build_hello(hello, sizeof(hello));
    if (n == 0) {
        ESP_LOGE(TAG, "viewer_accept: hello buffer overflow \u2014 proceeding");
    } else {
        esp_err_t hr = cam_stream_sink_send_text(hello, n);
        if (hr != ESP_OK) {
            ESP_LOGE(TAG, "viewer_accept: hello send failed: %s",
                     esp_err_to_name(hr));
        }
    }

    ESP_LOGI(TAG,
             "viewer accepted fd=%d (sink=server-side, hello=%u bytes)",
             fd, (unsigned)n);
    return ESP_OK;
}

/* ---------- post-handshake drop path ---------- */

static esp_err_t ws_cams_drain_frame(httpd_req_t *req)
{
    httpd_ws_frame_t pkt = {0};
    if (httpd_ws_recv_frame(req, &pkt, 0) != ESP_OK) {
        return ESP_OK;
    }
    if (pkt.len == 0) {
        return ESP_OK;
    }

    uint8_t  scratch[64];
    size_t   remaining = pkt.len;
    while (remaining > 0) {
        size_t want = (remaining < sizeof(scratch))
                          ? remaining : sizeof(scratch);
        httpd_ws_frame_t drain = {0};
        drain.len     = want;
        drain.payload = scratch;
        if (httpd_ws_recv_frame(req, &drain, want) != ESP_OK) {
            return ESP_OK;
        }
        remaining -= want;
    }
    return ESP_OK;
}

/* ---------- public surface ---------- */

esp_err_t ws_cams_register_uri(httpd_handle_t hd)
{
    if (hd == NULL) {
        return ESP_ERR_INVALID_ARG;
    }
    if (s_uri_registered) {
        ESP_LOGW(TAG, "register: already registered \u2014 ignoring");
        return ESP_OK;
    }

    static const httpd_uri_t ws_uri = {
        .uri          = WS_CAMS_URI_PATH,
        .method       = HTTP_GET,
        .handler      = ws_cams_handler,
        .user_ctx     = NULL,
        .is_websocket = true,
    };
    esp_err_t r = httpd_register_uri_handler(hd, (httpd_uri_t *)&ws_uri);
    if (r != ESP_OK) {
        ESP_LOGE(TAG, "register %s failed: %s",
                 WS_CAMS_URI_PATH, esp_err_to_name(r));
        return r;
    }
    s_uri_registered = true;
    ESP_LOGI(TAG, "%s registered (is_websocket=true)", WS_CAMS_URI_PATH);
    return ESP_OK;
}

esp_err_t ws_cams_handler(httpd_req_t *req)
{
    if (req == NULL) {
        return ESP_FAIL;
    }
    if ((int)req->method == (int)HTTP_GET) {
        return viewer_accept(req);
    }
    return ws_cams_drain_frame(req);
}

void ws_cams_reset_for_test(void)
{
    s_httpd         = NULL;
    s_uri_registered = false;
    s_viewer_fd     = -1;
    cam_stream_sink_install(NULL);
}

int ws_cams_viewer_fd_get(void)
{
    return s_viewer_fd;
}

bool ws_cams_is_uri_registered(void)
{
    return s_uri_registered;
}

/* W7 hook — populated by W7.
 * Called by provisioning/src/ws_cams.c's WIFI_EVENT_STA_DISCONNECTED
 * subscriber after the wifi event fires.
 *
 * Today the auto-clear path is the async-send failure in
 * server_sink_send_bin / _text; this function only exists so
 * W7 can drop the sink + viewer slot explicitly when the STA
 * link falls off. It is safe to call multiple times. */
void ws_cams_on_sta_disconnected(void)
{
    if (s_viewer_fd >= 0 || s_uri_registered) {
        int fd = s_viewer_fd;
        s_viewer_fd     = -1;
        s_uri_registered = false;
        cam_stream_sink_install(NULL);
        ESP_LOGI(TAG,
                 "STA disconnected: cleared viewer slot fd=%d, "
                 "URI re-register on next IP-up",
                 fd);
    }
}

/* ---------- W7 — WiFi lifecycle subscribers ---------- */

/* IP_EVENT_STA_GOT_IP handler. The first IP-up after
 * provisioning_start_sta_server also gets the URI registered
 * (idempotent if the same handle is reused). On every
 * subsequent IP-up after a transient STA disconnect the
 * URI is registered on the live handle again, defensively
 * (iot_cams's httpd is long-lived today, but the pattern
 * matches the reference's documented fix for the same
 * class of bug). */
static void ws_cams_on_got_ip(void *arg, esp_event_base_t event_base,
                              int32_t event_id, void *event_data)
{
    (void)arg;
    (void)event_base;
    (void)event_id;
    (void)event_data;

    /* In iot_cams, the httpd outlives the STA netif: once
     * provisioning_start_sta_server() succeeded, the same
     * handle is re-used across every reconnect. Subscribing
     * to IP_EVENT_STA_GOT_IP is defensive — it covers the
     * case where the STA httpd is later moved to a softAP-
     * listener pattern (mirrors the reference) or killed by
     * a future provisioning flow. */
    ESP_LOGI(TAG, "IP_EVENT_STA_GOT_IP fired; /ws/cams registration "
                  "remains live on the long-lived httpd");
    (void)ws_cams_register_uri(s_httpd);
}

/* WIFI_EVENT_STA_DISCONNECTED handler — proper 4-arg
 * signature so esp_event_handler_register stores it
 * directly without a cast. The handler delegates to the
 * same ws_cams_on_sta_disconnected() that `sta_got_ip`
 * uses. */
static void ws_cams_on_sta_disconnected_event(
    void *arg, esp_event_base_t event_base,
    int32_t event_id, void *event_data)
{
    (void)arg;
    (void)event_base;
    (void)event_id;
    (void)event_data;
    ws_cams_on_sta_disconnected();
}

/* Subscribe to IP_EVENT_STA_GOT_IP and WIFI_EVENT_STA_DISCONNECTED.
 * Idempotent — safe to call once from provisioning_start_sta_server
 * after the httpd handle is live.
 *
 * The WIFI_EVENT_STA_DISCONNECTED subscriber clears the viewer
 * slot + sink so a vanished viewer can't deadlock the slot. The
 * IP_EVENT_STA_GOT_IP subscriber logs only (and defensively
 * re-registers the URI). */
esp_err_t ws_cams_install(void)
{
    esp_err_t r = esp_event_handler_register(
        IP_EVENT, IP_EVENT_STA_GOT_IP,
        ws_cams_on_got_ip, NULL);
    if (r != ESP_OK) return r;
    return esp_event_handler_register(
        WIFI_EVENT, WIFI_EVENT_STA_DISCONNECTED,
        ws_cams_on_sta_disconnected_event, NULL);
}
