/* provisioning.c — implementation home of the iot_cams WiFi
 * provisioning component.
 *
 * T1 SCOPE (this commit):
 *   - Compile-only stubs of every public-API function.
 *   - Bring-up arrives in T2.
 *
 * The stub returns are deliberately conservative: ESP_OK where
 * the contract is "no error", and the documented error codes
 * where the contract rejects input. Reason: a future caller
 * reading the header should see behavior consistent with the
 * final implementation from day one, even before real body
 * lands.
 */
#include "provisioning.h"

#include <string.h>
#include "esp_log.h"

static const char *TAG = "prov";

/* Module-private state. Lives in .bss; single-threaded during
 * provisioning so a mutex is not required. provisioning_init()
 * zeroes and populates it on first call. */
static struct {
    bool                    initialized;
    bool                    manager_running;
    provisioning_security_t security;
    char                    pop[PROV_POP_MAX_LEN + 1];
    char                    service_name[PROV_NAME_MAX_LEN + 1];
    provisioning_app_info_t app_info;
} s_prov;

static bool copy_bounded(char *dst, size_t dst_size, const char *src,
                         size_t src_len)
{
    /* strncpy + explicit null-terminator. dst_size must be > 0. */
    if (dst_size == 0) return false;
    if (src_len >= dst_size) return false;
    memcpy(dst, src, src_len);
    dst[src_len] = '\0';
    return true;
}

esp_err_t provisioning_init(const provisioning_config_t *cfg,
                            const provisioning_app_info_t *app_info)
{
    if (!cfg) {
        ESP_LOGE(TAG, "init: cfg NULL");
        return ESP_ERR_INVALID_ARG;
    }
    if (s_prov.initialized) {
        ESP_LOGE(TAG, "init: already initialized");
        return ESP_ERR_INVALID_STATE;
    }

    memset(&s_prov, 0, sizeof(s_prov));
    s_prov.security = cfg->security;

    if (cfg->pop_len > 0) {
        if (!copy_bounded(s_prov.pop, sizeof(s_prov.pop),
                          cfg->pop, cfg->pop_len)) {
            ESP_LOGE(TAG, "init: PoP exceeds %d bytes",
                     (int)sizeof(s_prov.pop) - 1);
            return ESP_ERR_INVALID_ARG;
        }
    } else {
        const char *default_pop = CONFIG_PROVISIONING_POP;
        if (!copy_bounded(s_prov.pop, sizeof(s_prov.pop),
                          default_pop, strlen(default_pop))) {
            /* default pop "abcd1234" fits; copy_bounded only fails
             * on truly oversized defaults. */
            return ESP_ERR_INVALID_ARG;
        }
    }

    if (cfg->service_name[0] != '\0') {
        if (!copy_bounded(s_prov.service_name,
                          sizeof(s_prov.service_name),
                          cfg->service_name,
                          strlen(cfg->service_name))) {
            ESP_LOGE(TAG, "init: service_name exceeds %d bytes",
                     (int)sizeof(s_prov.service_name) - 1);
            return ESP_ERR_INVALID_ARG;
        }
    }
    /* Empty service_name is allowed; the runtime generates one
     * from CONFIG_PROVISIONING_SERVICE_NAME_PREFIX + MAC6. */

    if (app_info) {
        s_prov.app_info.name       = app_info->name;
        s_prov.app_info.fw_version = app_info->fw_version;
    }
    s_prov.initialized = true;
    ESP_LOGI(TAG, "init ok (security=%d)", (int)s_prov.security);
    return ESP_OK;
}

bool provisioning_is_provisioned(void)
{
    /* Stub: full read-through lands in T2 alongside the NVS
     * namespace contract. For now, report unprovisioned so the
     * boot branch enters provisioning. */
    return false;
}

esp_err_t provisioning_run(void)
{
    if (!s_prov.initialized) {
        ESP_LOGE(TAG, "run: not initialized");
        return ESP_ERR_INVALID_STATE;
    }
    if (s_prov.manager_running) {
        ESP_LOGW(TAG, "run: manager already running");
        return ESP_ERR_INVALID_STATE;
    }
    s_prov.manager_running = true;
    ESP_LOGI(TAG, "run: provisioning begin (stub — T2 lands the manager)");
    /* Real body in T2: init netif/event-loop/NVS, esp_wifi_init,
     * mDNS init, wifi_prov_mgr_init(WIFI_PROV_SCHEME_SOFTAP, ...),
     * wifi_prov_mgr_start_provisioning(), wifi_prov_mgr_wait(). */
    s_prov.manager_running = false;
    return ESP_OK;
}

void provisioning_stop(void)
{
    if (!s_prov.manager_running) {
        ESP_LOGW(TAG, "stop: nothing to stop");
        return;
    }
    ESP_LOGI(TAG, "stop: stub (T2 routes via wifi_prov_mgr_stop_provisioning)");
    s_prov.manager_running = false;
}

esp_err_t provisioning_reset_credentials(void)
{
    /* Stub: T3 wipes the `prov_cfg` NVS namespace + the esp_wifi
     * storage. For now we just log. */
    ESP_LOGI(TAG, "reset: stub (T3 lands the NVS wipe)");
    return ESP_OK;
}
