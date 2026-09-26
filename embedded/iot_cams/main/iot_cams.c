/* iot_cams.c — application entry point (app_main).
 *
 * Sole responsibility: bootstrap the WiFi provisioning package and
 * wait for credentials to land. Once credentials are committed to
 * NVS, the firmware hands control to the IDF event loop and waits
 * for supervision tasks that the follow-up work (camera pipeline,
 * WebSocket control plane, factory-reset, ...) will spin up.
 *
 * Constraints enforced by this file:
 *   - It includes ONLY `provisioning.h`. No `esp_wifi.h`,
 *     `esp_netif.h`, `mdns.h` or `protocomm.h` shows up here
 *     — those are package-private. If you find yourself wanting
 *     to add such an include, it is a sign the package surface
 *     is too narrow and should be widened.
 *   - It hardcodes a single Kconfig-fixed deployment: security=1
 *     with the PoP from CONFIG_PROVISIONING_POP, service_name
 *     derived from CONFIG_PROVISIONING_SERVICE_NAME_PREFIX +
 *     MAC. A factory-reset flow, alternative security levels,
 *     or alternative transports are deliberate work for
 *     follow-up components.
 */
#include <stdio.h>

#include "esp_log.h"
#include "provisioning.h"

static const char *TAG = "app_main";

static const char *FW_VERSION = "0.1.0";

void app_main(void)
{
    /* Provisioning configuration. Zero-init so PoP and SSID fall
     * through to the Kconfig defaults (CONFIG_PROVISIONING_POP,
     * CONFIG_PROVISIONING_SERVICE_NAME_PREFIX). Force_provisioning
     * stays false — the future factory-reset path will toggle it. */
    provisioning_config_t cfg = {0};
    cfg.security = PROV_SECURITY_1;

    provisioning_app_info_t info = {
        .name       = CONFIG_PROVISIONING_DEVICE_NAME,
        .fw_version = FW_VERSION,
    };

    esp_err_t r = provisioning_init(&cfg, &info);
    if (r != ESP_OK) {
        ESP_LOGE(TAG, "provisioning_init failed: %s", esp_err_to_name(r));
        return;
    }

    ESP_LOGI(TAG, "device: %s fw=%s", info.name, info.fw_version);

    if (provisioning_is_provisioned()) {
        ESP_LOGI(TAG, "credentials present in NVS — joining AP");
        /* TODO(future): bring up the camera pipeline + ws plane
         * here once those components exist. For now we leave
         * the device on the IDF event loop with the station
         * attached to the AP the user already provisioned. */
        return;
    }

    ESP_LOGI(TAG, "no credentials in NVS — starting SoftAP provisioning");
    r = provisioning_run();
    if (r != ESP_OK) {
        ESP_LOGE(TAG, "provisioning_run failed: %s", esp_err_to_name(r));
        return;
    }

    /* Provisioning succeeded. Credentials are committed to NVS.
     * TODO(future): same as above — the application body lands
     * in a follow-up commit when the camera + ws + supervision
     * components exist. */
}
