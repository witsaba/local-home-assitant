/* sta_server.c — STA-bound HTTP server for post-provisioning device
 * identity and capture endpoints.
 *
 * PURPOSE
 *   After provisioning completes and the device has joined the home
 *   AP (STA mode), this module starts a lightweight httpd on the
 *   station interface. It exposes /whoami for device discovery and
 *   registration by the home-assistant backend, and /capture for
 *   taking single JPEG frames from the OV2640 sensor.
 *
 * LIFECYCLE
 *   - NOT started during softAP provisioning (the captive portal
 *     serves provisioning UX; /whoami + /capture are intentionally
 *     absent so unprovisioned devices don't appear in discovery
 *     and operators on the softAP can't pull frames before WiFi
 *     is configured).
 *   - Started by provisioning_join_ap() / provisioning_run() once
 *     the station has an IP.
 *   - Runs for the lifetime of the device.
 *
 * PORT
 *   Fixed port 80. The ESP32 station only serves local LAN clients
 *   (no inbound internet routing), so port 80 is safe and convenient.
 *
 * ENDPOINTS
 *   - GET /whoami  — device identity JSON (mac, name, description,
 *     fw, chip). Same contract as esp32-cam-surveillance /whoami
 *     for cross-device compatibility.
 *   - GET /capture — single JPEG frame from the OV2640. Mirrors the
 *     esp32-cam-surveillance /capture contract: image/jpeg body,
 *     Content-Disposition: inline; filename=capture.jpg, no chunking,
 *     no base64. Concurrency is guarded by a binary semaphore in
 *     cam_reader — concurrent callers past the 5 s wait budget get
 *     HTTP 503.
 *
 * SECURITY
 *   The device is on a trusted private LAN by design. No
 *   authentication on /whoami — the MAC is the canonical identity.
 *   /capture is similarly open because the LAN is trusted. Future
 *   endpoints (config, control) will add auth if needed.
 */
#include "sta_server.h"

#include <string.h>

#include "esp_log.h"
#include "esp_http_server.h"
#include "esp_mac.h"
#include "esp_chip_info.h"
#include "esp_system.h"
#include "esp_netif.h"
#include "esp_wifi.h"

#include "cam_reader.h"

static const char *TAG = "sta_srv";

/* The httpd handle. NULL when not running. */
static httpd_handle_t s_sta_httpd = NULL;

/* ---------- /whoami ---------- */

/* Convert 6-byte MAC to 12-char lowercase hex string (no separators).
 * out must be at least 13 bytes. Returns ESP_OK on success. */
static esp_err_t mac_to_hex_lower(const uint8_t mac[6], char *out, size_t out_len)
{
    if (!mac || !out || out_len < 13) {
        return ESP_ERR_INVALID_ARG;
    }
    for (int i = 0; i < 6; i++) {
        uint8_t b = mac[i];
        uint8_t hi = (b >> 4) & 0x0F;
        out[i * 2] = (hi < 10) ? ('0' + hi) : ('a' + hi - 10);
        uint8_t lo = b & 0x0F;
        out[i * 2 + 1] = (lo < 10) ? ('0' + lo) : ('a' + lo - 10);
    }
    out[12] = '\0';
    return ESP_OK;
}

/* GET /whoami — JSON device identity.
 * Returns: mac, name, description (omitted if empty), fw, chip
 * Follows the esp32-cam-surveillance /whoami contract.
 * Optimized: static buffer, no heap, no per-request logging. */
static esp_err_t whoami_get_handler(httpd_req_t *req)
{
    if (!req) return ESP_FAIL;

    httpd_resp_set_type(req, "application/json");
    httpd_resp_set_hdr(req, "Cache-Control", "no-store");

    /* Read MAC from eFuse. */
    uint8_t mac[6] = {0};
    esp_err_t r = esp_read_mac(mac, ESP_MAC_WIFI_STA);
    if (r != ESP_OK) {
        ESP_LOGE(TAG, "whoami: esp_read_mac failed: %s", esp_err_to_name(r));
        httpd_resp_set_status(req, "500 Internal Server Error");
        httpd_resp_sendstr(req, "{\"error\":\"mac_unavailable\"}");
        return ESP_OK;
    }

    /* Convert MAC to lowercase hex string (12 chars, no separators). */
    char mac_hex[13];
    mac_to_hex_lower(mac, mac_hex, sizeof(mac_hex));

    /* Read chip info. */
    esp_chip_info_t chip;
    esp_chip_info(&chip);

    /* chip model -> human-readable string. */
    const char *chip_str = "ESP32-UNKNOWN";
    switch (chip.model) {
        case 1:  chip_str = "ESP32-D0WDQ6"; break;
        case 2:  chip_str = "ESP32-S2";     break;
        case 5:  chip_str = "ESP32-S3";     break;
        case 12: chip_str = "ESP32-C3";     break;
        default: chip_str = "ESP32-UNKNOWN"; break;
    }

    /* Device name from Kconfig. */
    extern const char *prov_device_name(void);
    const char *name = prov_device_name();
    const char *description = "";  /* no identity NVS yet */

    /* Build JSON response using static buffer (no heap allocation).
     * Format: {"mac":"...","name":"...","fw":"...","chip":"..."}
     * description is omitted when empty (saves 14 bytes). */
    char buf[160];
    int len;
    if (description[0] == '\0') {
        /* Omit description when empty. */
        len = snprintf(buf, sizeof(buf),
            "{\"mac\":\"%s\",\"name\":\"%s\",\"fw\":\"%s\",\"chip\":\"%s\"}",
            mac_hex, name, esp_get_idf_version(), chip_str);
    } else {
        len = snprintf(buf, sizeof(buf),
            "{\"mac\":\"%s\",\"name\":\"%s\",\"description\":\"%s\",\"fw\":\"%s\",\"chip\":\"%s\"}",
            mac_hex, name, description, esp_get_idf_version(), chip_str);
    }

    if (len < 0 || (size_t)len >= sizeof(buf)) {
        httpd_resp_set_status(req, "500 Internal Server Error");
        httpd_resp_sendstr(req, "{\"error\":\"buf_overflow\"}");
        return ESP_OK;
    }

    httpd_resp_set_hdr(req, "X-Witsaba-Device", "true");
    httpd_resp_send(req, buf, len);
    return ESP_OK;
}

/* ---------- /capture ---------- */

/* GET /capture — single JPEG frame from the OV2640.
 *
 * Concurrency contract: cam_reader owns the camera mutex; this
 * handler maps the three documented outcomes to HTTP status codes:
 *
 *   - ESP_ERR_TIMEOUT   → 503 (mutex not acquired; do NOT call
 *                         cam_reader_release on this path)
 *   - sensor failure    → 500 + cam_reader_release(NULL) so the
 *                         mutex is given back to the driver pool
 *   - success           → 200 + httpd_resp_send + cam_reader_release(fb)
 *
 * The mutex MUST be released on every path that took it. The helper
 * makes that explicit: cam_reader_release(NULL) is permitted and
 * only gives the mutex (the buffer return becomes a no-op). */
static esp_err_t capture_get_handler(httpd_req_t *req)
{
    if (req == NULL) {
        return ESP_FAIL;
    }

    camera_fb_t *fb = NULL;
    esp_err_t r = cam_reader_capture(&fb);

    if (r == ESP_ERR_TIMEOUT) {
        ESP_LOGW(TAG, "capture: mutex timeout; another caller busy");
        httpd_resp_set_status(req, "503 Service Unavailable");
        httpd_resp_set_type(req, "text/plain");
        httpd_resp_sendstr(req, "Camera busy, please try again");
        return ESP_OK;
    }
    if (r != ESP_OK || fb == NULL) {
        ESP_LOGE(TAG, "capture: sensor returned no frame");
        httpd_resp_send_500(req);
        cam_reader_release(NULL);  /* drop the mutex */
        return ESP_FAIL;
    }

    httpd_resp_set_type(req, "image/jpeg");
    httpd_resp_set_hdr(req, "Content-Disposition",
                       "inline; filename=capture.jpg");
    httpd_resp_set_hdr(req, "Cache-Control",
                       "no-store, no-cache, must-revalidate, max-age=0");

    esp_err_t res = httpd_resp_send(req, (const char *)fb->buf, fb->len);
    cam_reader_release(fb);

    if (res == ESP_OK) {
        ESP_LOGD(TAG, "capture: served frame");
    } else {
        ESP_LOGE(TAG, "capture: httpd_resp_send: %s", esp_err_to_name(res));
    }
    return res;
}

/* ---------- lifecycle ---------- */

esp_err_t sta_server_start(void)
{
    if (s_sta_httpd) {
        ESP_LOGW(TAG, "sta_server already running");
        return ESP_OK;
    }

    httpd_config_t cfg = HTTPD_DEFAULT_CONFIG();
    cfg.server_port = 80;
    cfg.stack_size = 4096;
    cfg.max_uri_handlers = 8;
    cfg.max_req_hdr_len = 512;

    esp_err_t err = httpd_start(&s_sta_httpd, &cfg);
    if (err != ESP_OK) {
        ESP_LOGE(TAG, "httpd_start failed: %s", esp_err_to_name(err));
        s_sta_httpd = NULL;
        return err;
    }

    /* Register /whoami. */
    httpd_uri_t whoami_uri = {
        .uri       = "/whoami",
        .method    = HTTP_GET,
        .handler   = whoami_get_handler,
        .user_ctx  = NULL,
    };
    err = httpd_register_uri_handler(s_sta_httpd, &whoami_uri);
    if (err != ESP_OK) {
        ESP_LOGE(TAG, "register /whoami failed: %s", esp_err_to_name(err));
        httpd_stop(s_sta_httpd);
        s_sta_httpd = NULL;
        return err;
    }

    /* Register /capture. Same handle, same audience (post-
     * provisioning LAN clients). Concurrency guard owned by
     * cam_reader; the handler here is a thin wrapper that maps
     * the cam_reader_capture outcomes to HTTP status codes. */
    httpd_uri_t capture_uri = {
        .uri       = "/capture",
        .method    = HTTP_GET,
        .handler   = capture_get_handler,
        .user_ctx  = NULL,
    };
    err = httpd_register_uri_handler(s_sta_httpd, &capture_uri);
    if (err != ESP_OK) {
        ESP_LOGE(TAG, "register /capture failed: %s", esp_err_to_name(err));
        httpd_stop(s_sta_httpd);
        s_sta_httpd = NULL;
        return err;
    }

    ESP_LOGI(TAG, "STA server running on port 80, /whoami and /capture registered");
    return ESP_OK;
}

void sta_server_stop(void)
{
    if (!s_sta_httpd) {
        return;
    }
    esp_err_t err = httpd_stop(s_sta_httpd);
    if (err != ESP_OK) {
        ESP_LOGW(TAG, "httpd_stop: %s", esp_err_to_name(err));
    }
    s_sta_httpd = NULL;
    ESP_LOGI(TAG, "STA server stopped");
}

bool sta_server_is_running(void)
{
    return s_sta_httpd != NULL;
}
