/* ws_cams.c — W4 handler skeleton + module-static state.
 *
 * The `/ws/cams` WebSocket endpoint lives on the SAME httpd
 * handle as `/whoami` and `/capture` (registered by
 * sta_server.c, which calls ws_cams_register_uri() at server
 * start). Server-mode WS:
 *   - one inbound viewer at a time (mirror coverage lands in
 *     W6 — for now every handshake accepts and overwrites the
 *     stored fd; multi-handshake race fixed in W6).
 *   - one binary WS message per JPEG frame (cam_stream task
 *     pushes the bytes; the sink is wired in W5).
 *   - intermittent JSON hello + status frames (text; built in
 *     cam_stream_wire.c, emitted in W5).
 *
 * What this file lands in W4:
 *   - WS endpoint registration helper (ws_cams_register_uri),
 *     called by sta_server_start() once the httpd handle is
 *     live.
 *   - handshake accept handler — captures `s_viewer_fd` (so
 *     later work-units can probe it), logs "viewer accepted",
 *     returns ESP_OK so the httpd completes the 101 upgrade.
 *   - post-handshake drop path for inbound frames (silently
 *     ignored; no control-plane parsing yet — that arrives in
 *     the {"cmd":"stream"} follow-up branch).
 *
 * What this file does NOT do in W4:
 *   - install the cam_stream_sink_t on handshake (W5)
 *   - emit the hello JSON on handshake (W5)
 *   - enforce single-viewer rejection (W6)
 *   - re-attach on IP_EVENT_STA_GOT_IP (W7)
 *
 * Module-static state is left at `-1` (no viewer) until the
 * first handshake accepts.
 *
 * URL path: hard-coded "/ws/cams" in W4 (the W8 Kconfig knob
 * CONFIG_FIRMWARE_WS_PATH swaps in once all knobs land in one
 * commit, keeping each work-unit self-consistent).
 */

#include <stddef.h>

#include "esp_log.h"
#include "esp_http_server.h"

#include "ws_cams.h"

#define TAG "ws_cams"

/* W4 hard-coded path; W8 routes through CONFIG_FIRMWARE_WS_PATH. */
#define WS_CAMS_URI_PATH "/ws/cams"

/* Captured at handshake accept time so W5 can call
 * httpd_ws_send_frame_async(hd, fd, ...) on the captured
 * pair without re-reading req->server/req->to_sockfd on
 * every frame. -1 = no viewer right now. */
static int  s_viewer_fd = -1;

/* Registered state, idempotent. */
static bool s_uri_registered = false;

esp_err_t ws_cams_register_uri(httpd_handle_t hd)
{
    if (hd == NULL) {
        return ESP_ERR_INVALID_ARG;
    }
    if (s_uri_registered) {
        ESP_LOGW(TAG, "register: already registered — ignoring");
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

/* Handshake accept. IDF has completed the 101 upgrade before
 * invoking us; we just need to capture the fd and return ESP_OK.
 *
 * W4 placeholder behavior:
 *   - replace the captured fd on every handshake (any later
 *     handshake from a different peer is currently a no-op
 *     overwrite — W6 enforces "second viewer gets
 *     viewer_limit" by comparing the new fd to the captured
 *     fd and returning ESP_FAIL on a different one).
 *   - the cam_stream sink is NOT installed yet (W5). */
static esp_err_t viewer_accept(httpd_req_t *req)
{
    int fd = httpd_req_to_sockfd(req);
    if (fd < 0) {
        ESP_LOGE(TAG, "viewer_accept: httpd_req_to_sockfd failed");
        return ESP_FAIL;
    }
    s_viewer_fd = fd;
    ESP_LOGI(TAG, "viewer accepted fd=%d (W4 placeholder; W5 installs sink)",
             fd);
    return ESP_OK;
}

/* Post-handshake inbound frames. W4 ignores every frame:
 *   - PING/PONG/CLOSE/BINARY/empty    — drop silently (will
 *     receive a hello + binary frames from the stream task
 *     in W5 onward)
 *   - TEXT (the future {"cmd":"stream"} control plane)  —
 *     drop without parsing (landed in a follow-up branch,
 *     see odd/tasks/ws-cams-endpoint.md "Out of scope")
 *
 * Every post-handshake frame is `recv_frame(pkt, 0)` to drain
 * the length probe, then `recv_frame(pkt, pkt.len)` to fetch
 * the payload (or chunked-drain if we don't care). For now the
 * simple shape: length probe, then accept-and-discard the
 * payload via a stack scratch buffer.
 *
 * W4 contract: never fail the connection post-handshake. */
static esp_err_t ws_cams_drain_frame(httpd_req_t *req)
{
    httpd_ws_frame_t pkt = {0};
    if (httpd_ws_recv_frame(req, &pkt, 0) != ESP_OK) {
        /* Stream desync; the httpd worker has nothing else to
         * do. Returning ESP_OK keeps the connection alive. */
        return ESP_OK;
    }
    if (pkt.len == 0) {
        return ESP_OK;
    }

    /* Bound the per-frame drain. 64 B scratches >99% of
     * expected text control frames; if a frame is longer we
     * chunk-drain it. */
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

/* Test seam. Clears the registered flag and the captured fd so
 * the next ws_cams_register_uri() call re-registers cleanly. */
void ws_cams_reset_for_test(void)
{
    s_uri_registered = false;
    s_viewer_fd      = -1;
}

int ws_cams_viewer_fd_get(void)
{
    return s_viewer_fd;
}

bool ws_cams_is_uri_registered(void)
{
    return s_uri_registered;
}
