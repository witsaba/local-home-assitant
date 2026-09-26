/* captive_portal.h — captive-portal HTML form for the iot_cams
 * provisioning component.
 *
 * Replaces the protocomm-driven phone-app flow with a
 * browser-driven flow that's reachable on a phone that has
 * only the softAP Wi-Fi (no internet access). Operators type
 * the AP credentials in an HTML form, POST it to the device,
 * and the device applies them with the same wifi_prov_mgr
 * machinery the protocomm path uses.
 *
 * SCOPE — what this header exposes to `provisioning.c`:
 *
 *   captive_portal_bring_up()
 *     Brings up an httpd server, registers the four captive
 *     URIs (GET /, POST /provision, GET /whoami, default
 *     handler), and hands the httpd handle to the softAP
 *     scheme so the manager's protocomm URIs are layered on
 *     the same port. Idempotent.
 *
 *   captive_portal_tear_down()
 *     Stops the httpd and clears the registered handlers.
 *
 * THREADING — the bring-up must be called from app_main or a
 * single boot-time thread (NOT from a task); the teardown
 * likewise. The HTTP requests themselves are dispatched by
 * the httpd's worker thread.
 *
 * SECURITY — the WPA2 passphrase that gates the softAP
 * network is the operator-visible security boundary. The PoP
 * used by the protocomm security-1 path is NOT shown to the
 * operator; a deployment that needs it falls back to the
 * protocomm path (CONFIG_PROVISIONING_USE_CAPTIVE_PORTAL=n).
 */
#ifndef IOT_CAMS_CAPTIVE_PORTAL_H
#define IOT_CAMS_CAPTIVE_PORTAL_H

#include "esp_err.h"
#include "esp_http_server.h"

#ifdef __cplusplus
extern "C" {
#endif

/* httpd handle owned by the captive portal. Exposed so the
 * softAP scheme can register its protocomm URIs on the same
 * server (no port 80 collision). */
extern httpd_handle_t s_captive_httpd;

/* Title shown in the HTML form's <h1>. Configurable at build
 * time via Kconfig; defaults to the device name. */
esp_err_t captive_portal_bring_up(void);

/* Stops the captive httpd and clears the registered URIs. Safe
 * to call from the boot thread; not safe to call from an httpd
 * worker thread (would deadlock). */
void captive_portal_tear_down(void);

#ifdef __cplusplus
}
#endif
#endif /* IOT_CAMS_CAPTIVE_PORTAL_H */
