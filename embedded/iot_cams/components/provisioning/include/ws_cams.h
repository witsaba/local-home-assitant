/* ws_cams.h — public surface for the /ws/cams WS endpoint
 * handler (W4 of feat/iot-cams-ws-cams-endpoint). Lives in the
 * provisioning component because the WS endpoint rides on the
 * same httpd handle that hosts /whoami and /capture.
 *
 * The component keeps the seam tight: only registration and
 * a few diagnostic accessors are public, so sta_server.c (which
 * drives the httpd lifecycle) is the only caller of the
 * registration helper. Test seams are surfaced via
 * ws_cams_reset_for_test().
 */

#ifndef IOT_CAMS_WS_CAMS_H
#define IOT_CAMS_WS_CAMS_H

#include <stdbool.h>

#include "esp_err.h"
#include "esp_http_server.h"

#ifdef __cplusplus
extern "C" {
#endif

/* Register the GET /ws/cams URI handler on `hd`.
 *
 * Idempotent (a second call on the same handle is a no-op).
 * Returns ESP_ERR_INVALID_ARG on a NULL handle; the httpd
 * register error otherwise. */
esp_err_t ws_cams_register_uri(httpd_handle_t hd);

/* The handler that backs the URI; exported so sta_server.c
 * can register it via the static httpd_uri_t above. Most
 * callers never invoke this directly. */
esp_err_t ws_cams_handler(httpd_req_t *req);

/* Host-test reset + diagnostic accessors. */
void ws_cams_reset_for_test(void);
int  ws_cams_viewer_fd_get(void);
bool ws_cams_is_uri_registered(void);

/* W7 hook — provisioning's STA-disconnected event subscriber
 * calls this to clear the viewer slot + sink so the next
 * IP_EVENT_STA_GOT_IP re-attach starts from a clean slate. */
void ws_cams_on_sta_disconnected(void);

#ifdef __cplusplus
}
#endif

#endif /* IOT_CAMS_WS_CAMS_H */
