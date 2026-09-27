/* sta_server.h — STA-bound HTTP server for post-provisioning
 * device identity endpoints.
 *
 * PURPOSE
 *   Starts an httpd on the station interface after the device
 *   has joined the home AP. Exposes /whoami for device discovery
 *   and registration.
 *
 * USAGE
 *   Call sta_server_start() after the device has an IP from
 *   the home AP (after provisioning_join_ap() succeeds and
 *   IP_EVENT_STA_GOT_IP fires). The server runs for the lifetime
 *   of the device.
 *
 *   Call sta_server_stop() to tear down the server if needed.
 */
#ifndef IOT_CAMS_STA_SERVER_H
#define IOT_CAMS_STA_SERVER_H

#include <stdbool.h>
#include "esp_err.h"

#ifdef __cplusplus
extern "C" {
#endif

/* Start the STA-bound httpd server on port 80.
 * Registers GET /whoami for device identity.
 *
 * Idempotent: if already running, returns ESP_OK without
 * starting a second server.
 *
 * Returns ESP_OK on success, ESP_ERR_NO_MEM if the httpd
 * cannot be allocated, or the underlying httpd_start error. */
esp_err_t sta_server_start(void);

/* Stop the STA-bound httpd server.
 * Safe to call even if not running (no-op). */
void sta_server_stop(void);

/* Returns true if the STA server is currently running. */
bool sta_server_is_running(void);

#ifdef __cplusplus
}
#endif
#endif /* IOT_CAMS_STA_SERVER_H */
