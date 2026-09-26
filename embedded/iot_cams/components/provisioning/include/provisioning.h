/* provisioning.h — public surface for the iot_cams WiFi provisioning
 * component.
 *
 * The component wraps ESP-IDF v5.5 `wifi_provisioning` (SoftAP scheme,
 * security 1 by default) and hides every IDF-specific include from
 * the rest of the firmware. Application code keeps three lines:
 *
 *     provisioning_init(&cfg, &info);   // once per boot
 *     if (!provisioning_is_provisioned()) provisioning_run();
 *
 * Internals — protocomm, mDNS, esp_wifi, the SoftAP HTTP server — are
 * not part of the public surface. If you find yourself wanting to
 * include them from app_main, you are reaching past the package.
 *
 * PERSISTENCE — the package owns the `prov_cfg` NVS namespace:
 *   - svc_name   (string)  SoftAP SSID in effect at run time
 *   - pop        (string)  Proof-of-possession in effect at run time
 *   - sec        (uint8)   Security level (0 or 1)
 *
 * The Wi-Fi credentials (ssid + password) live in the namespace
 * esp_wifi itself uses; the IDF manager writes there during the
 * apply_config flow. This split is intentional and matches
 * esp-provisioning's docs.
 *
 * THREADING — provisioning_init() must be called from app_main
 * (single thread) before provisioning_run(). provisioning_run()
 * blocks. provisioning_stop() may be called from any task to
 * trigger teardown; wifi_prov_mgr_stop_provisioning schedules the
 * stop on its own task and emits WIFI_PROV_END.
 *
 * LIFTING — to publish this component as its own upstream / repo
 * later, move the component directory, register the new URL via
 * the consumer's `idf_component.yml`, and replace the consumer's
 * `path:` dep with the URL. Nothing in this header changes.
 */
#ifndef IOT_CAMS_PROVISIONING_H
#define IOT_CAMS_PROVISIONING_H

#include <stddef.h>
#include <stdbool.h>
#include "esp_err.h"

#ifdef __cplusplus
extern "C" {
#endif

#define PROV_POP_MAX_LEN    32   /* max bytes of proof-of-possession */
#define PROV_NAME_MAX_LEN   32   /* max bytes of softAP SSID */
#define PROV_VERSION_MAX    16   /* max bytes exposed as fw_version */

typedef enum {
    PROV_SECURITY_0 = 0,           /* plain text — debug only */
    PROV_SECURITY_1 = 1,           /* X25519 + PoP + AES-CTR — shipping default */
} provisioning_security_t;

typedef struct {
    /* Skip the is_provisioned() early-out and force provisioning
     * to run. Use this from the factory-reset flow. */
    bool     force_provisioning;

    /* Security level. PROV_SECURITY_1 strongly recommended. */
    provisioning_security_t security;

    /* Proof-of-possession (PoP), used with SECURITY_1. Must be
     * null-terminated. Leave empty to fall back to
     * CONFIG_PROVISIONING_POP at runtime. */
    char     pop[PROV_POP_MAX_LEN + 1];
    size_t   pop_len;

    /* SoftAP SSID. Must be null-terminated. If empty, the
     * component constructs "{CONFIG_PROVISIONING_SERVICE_NAME_PREFIX}_{MAC6}". */
    char     service_name[PROV_NAME_MAX_LEN + 1];
} provisioning_config_t;

/* Application-level identity exposed on the optional
 * `iot-cam-info` custom protocomm endpoint. NULL fields are
 * serialized as empty strings. */
typedef struct {
    const char *name;
    const char *fw_version;
} provisioning_app_info_t;

/* One-shot initializer. Safe to call once per boot before
 * provisioning_run(). cfg must not be NULL. app_info may be
 * NULL — the iot-cam-info endpoint simply returns an empty body
 * if not provided.
 *
 * Returns ESP_OK on success, ESP_ERR_INVALID_ARG if cfg is NULL
 * or a string field is longer than its budget, or ESP_ERR_INVALID_STATE
 * if the package was already initialized. */
esp_err_t provisioning_init(const provisioning_config_t *cfg,
                            const provisioning_app_info_t *app_info);

/* True iff Wi-Fi credentials are present in NVS (managed by
 * esp_wifi_set_storage(WIFI_STORAGE_FLASH)). This is the fast
 * path; the manager reads from the same store during the next
 * provisioning init. */
bool provisioning_is_provisioned(void);

/* Blocking bring-up. Initializes the default event loop, NVS,
 * esp_netif, esp_wifi (APSTA so the station can connect to the
 * AP the user just provisioned), and mDNS; then calls
 * wifi_prov_mgr_start_provisioning() and
 * wifi_prov_mgr_wait(). Returns when the manager emits
 * WIFI_PROV_END.
 *
 * Returns ESP_OK on a successful provisioning session, or the
 * underlying esp_err_t on bring-up failure (logged with the
 * failing step + esp_err_to_name). Returns ESP_ERR_INVALID_STATE
 * if the package is not initialized or
 * force_provisioning is not set and credentials already exist. */
esp_err_t provisioning_run(void);

/* Trigger an in-progress stop on the manager's own task. Safe
 * to call from a task other than the one blocked in
 * provisioning_run(). Idempotent while the manager is idle. */
void provisioning_stop(void);

/* Wipe the `prov_cfg` namespace + the esp_wifi credentials. After
 * this call provisioning_is_provisioned() returns false and the
 * next provisioning_run() will start the SoftAP. Used by the
 * factory-reset flow. */
esp_err_t provisioning_reset_credentials(void);

#ifdef __cplusplus
}
#endif
#endif /* IOT_CAMS_PROVISIONING_H */
