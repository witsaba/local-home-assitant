/* provisioning.c — implementation home of the iot_cams WiFi
 * provisioning component.
 *
 * SCOPE — clean captive-portal HTML form for offline /
 * private-network deployments:
 *
 *   - Bring up the softAP on first boot (or whenever no
 *     credentials exist in NVS).
 *   - Run an httpd server with GET / (HTML form), POST
 *     /provision (apply Wi-Fi credentials), GET /whoami
 *     (JSON identity).
 *   - Block in provisioning_run() until the form posts
 *     valid credentials or the caller calls provisioning_stop().
 *   - Persist via esp_wifi_set_config(); the credentials
 *     land in esp_wifi's NVS storage automatically.
 *
 * We deliberately do NOT use IDF's wifi_provisioning /
 * protocomm manager for the captive-portal build. The
 * manager requires a separate phone-app install (which is
 * not available on offline deployments) and the
 * `set_httpd_handle` shared-server pattern crashed with
 * LoadProhibited in httpd_find_uri_handler on the device.
 * Direct esp_wifi + httpd is smaller, faster to verify, and
 * matches the operator flow.
 */
#include "provisioning.h"

#include <string.h>
#include <stdio.h>
#include <stdlib.h>
#include <stdint.h>

#include "esp_log.h"
#include "esp_event.h"
#include "esp_wifi.h"
#include "esp_netif.h"
#include "esp_mac.h"
#include "nvs_flash.h"
#include "mdns.h"
#include "freertos/FreeRTOS.h"
#include "freertos/semphr.h"

#include "captive_portal.h"

static const char *TAG = "prov";

/* Module-private state. Lives in .bss; initialization is
 * serialized through the boot thread. */
static struct {
    bool      initialized;
    bool      run_in_progress;
    char      service_name[PROV_NAME_MAX_LEN + 1];
} s_prov;

/* FreeRTOS semaphore that provisioning_run() blocks on.
 * Signalled by the captive POST /provision handler when
 * credentials have been applied (success), or by
 * provisioning_stop() (failure). Created on demand at the
 * start of provisioning_run(). */
static SemaphoreHandle_t s_done_sema = NULL;

/* Last outcome of the captive form submission. Flipped to
 * 'true' by the handler on a successful esp_wifi_set_config,
 * left as 'false' by provisioning_stop(). */
static volatile bool s_last_prov_success = false;

static bool copy_bounded(char *dst, size_t dst_size, const char *src,
                         size_t src_len)
{
    if (dst_size == 0) return false;
    if (src_len >= dst_size) return false;
    memcpy(dst, src, src_len);
    dst[src_len] = '\0';
    return true;
}

/* Build the runtime SoftAP SSID. If service_name is empty we
 * compose "{prefix}_{MAC3}" so two devices side by side don't
 * collide. The MAC suffix is the last three bytes of the base
 * MAC. */
static void derive_service_name(char *out, size_t out_size)
{
    if (out_size == 0) return;
    if (s_prov.service_name[0] != '\0') {
        copy_bounded(out, out_size, s_prov.service_name,
                     strlen(s_prov.service_name));
        return;
    }
    const char *prefix = CONFIG_PROVISIONING_SERVICE_NAME_PREFIX;
    uint8_t mac[6] = {0};
    if (esp_read_mac(mac, ESP_MAC_WIFI_STA) != ESP_OK) {
        memset(mac, 0, sizeof(mac));
    }
    snprintf(out, out_size, "%s_%02X%02X%02X",
             prefix, mac[3], mac[4], mac[5]);
}

/* Internal accessor for cross-module use (the captive
 * portal's /whoami handler). */
const char *internal_prov_ssid(void)
{
    static char buf[PROV_NAME_MAX_LEN + 1];
    derive_service_name(buf, sizeof(buf));
    return buf;
}

/* On the bring-up path we currently use esp_netif + esp_wifi
 * directly without a manager; we still need the netif + mDNS
 * idioms for the SoftAP service advertising. */
static esp_err_t softap_bring_up(void)
{
    esp_err_t r;

    r = esp_netif_init();
    if (r != ESP_OK && r != ESP_ERR_INVALID_STATE) {
        ESP_LOGE(TAG, "softap_bring_up: esp_netif_init: %s",
                 esp_err_to_name(r));
        return r;
    }

    r = esp_event_loop_create_default();
    if (r != ESP_OK && r != ESP_ERR_INVALID_STATE) {
        ESP_LOGE(TAG, "softap_bring_up: esp_event_loop_create_default: %s",
                 esp_err_to_name(r));
        return r;
    }

    esp_netif_t *ap_netif = esp_netif_create_default_wifi_ap();
    if (ap_netif == NULL) {
        ESP_LOGE(TAG, "softap_bring_up: default wifi ap netif NULL");
        return ESP_FAIL;
    }
    esp_netif_set_default_netif(ap_netif);

    wifi_init_config_t wifi_init_cfg = WIFI_INIT_CONFIG_DEFAULT();
    r = esp_wifi_init(&wifi_init_cfg);
    if (r != ESP_OK) {
        ESP_LOGE(TAG, "softap_bring_up: esp_wifi_init: %s",
                 esp_err_to_name(r));
        return r;
    }

    /* Mode = APSTA so we can complete the softAP advertise
     * and (later) attach to the user's station. The captive
     * portal flow drops the AP after a successful POST. */
    r = esp_wifi_set_mode(WIFI_MODE_APSTA);
    if (r != ESP_OK) {
        ESP_LOGE(TAG, "softap_bring_up: esp_wifi_set_mode(APSTA): %s",
                 esp_err_to_name(r));
        return r;
    }
    r = esp_wifi_start();
    if (r != ESP_OK) {
        ESP_LOGE(TAG, "softap_bring_up: esp_wifi_start: %s",
                 esp_err_to_name(r));
        return r;
    }

    r = mdns_init();
    if (r != ESP_OK) {
        ESP_LOGE(TAG, "softap_bring_up: mdns_init: %s",
                 esp_err_to_name(r));
        return r;
    }
    char hostname[PROV_NAME_MAX_LEN + 1] = {0};
    derive_service_name(hostname, sizeof(hostname));
    mdns_hostname_set(hostname);

    return ESP_OK;
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

    /* Idempotent NVS init — esp_wifi_set_config() requires
     * the wifi NVS storage to be ready. */
    {
        esp_err_t nv = nvs_flash_init();
        if (nv == ESP_ERR_NVS_NO_FREE_PAGES ||
            nv == ESP_ERR_NVS_NEW_VERSION_FOUND) {
            esp_err_t er = nvs_flash_erase();
            if (er != ESP_OK) {
                ESP_LOGE(TAG, "init: nvs_flash_erase: %s",
                         esp_err_to_name(er));
                return er;
            }
            nv = nvs_flash_init();
        }
        if (nv != ESP_OK && nv != ESP_ERR_INVALID_STATE) {
            ESP_LOGE(TAG, "init: nvs_flash_init: %s", esp_err_to_name(nv));
            return nv;
        }
    }

    memset(&s_prov, 0, sizeof(s_prov));

    if (cfg->pop_len > 0) {
        /* pop is accepted for API compatibility but the
         * captive flow does not surface a PoP prompt; the WPA2
         * passphrase is the operator-visible security gate. */
        ESP_LOGW(TAG, "init: captive flow ignores PoP arg (use softAP pass)");
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
    (void)cfg->security;
    (void)app_info;
    s_prov.initialized = true;
    ESP_LOGI(TAG, "init ok (captive-portal mode)");
    return ESP_OK;
}

bool provisioning_is_provisioned(void)
{
    if (!s_prov.initialized) {
        return false;
    }
    wifi_config_t cfg;
    if (esp_wifi_get_config(WIFI_IF_STA, &cfg) != ESP_OK) {
        return false;
    }
    return cfg.sta.ssid[0] != '\0';
}

esp_err_t provisioning_run(void)
{
    if (!s_prov.initialized) {
        ESP_LOGE(TAG, "run: not initialized");
        return ESP_ERR_INVALID_STATE;
    }
    if (s_prov.run_in_progress) {
        ESP_LOGW(TAG, "run: already in progress");
        return ESP_ERR_INVALID_STATE;
    }

    /* Fast-path: credentials already present, just confirm and
     * let the application continue. The actual station attach
     * happens in the post-provisioning application body, not
     * here. */
    if (provisioning_is_provisioned()) {
        ESP_LOGI(TAG, "run: credentials present in NVS — no softAP");
        return ESP_OK;
    }

    s_prov.run_in_progress = true;
    s_last_prov_success = false;

    esp_err_t r = softap_bring_up();
    if (r != ESP_OK) {
        s_prov.run_in_progress = false;
        return r;
    }

    /* Bring up the captive httpd server (port 80). */
    r = captive_portal_bring_up();
    if (r != ESP_OK) {
        ESP_LOGE(TAG, "run: captive_portal_bring_up: %s",
                 esp_err_to_name(r));
        s_prov.run_in_progress = false;
        return r;
    }

    char ssid[PROV_NAME_MAX_LEN + 1] = {0};
    derive_service_name(ssid, sizeof(ssid));
    ESP_LOGI(TAG, "captive portal live on %s (WPA2 passphrase from Kconfig)",
             ssid);

    /* Block on the semaphore until the captive form posts
     * credentials (signals s_done_sema with success) or the
     * caller signals via provisioning_stop(). */
    s_done_sema = xSemaphoreCreateBinary();
    if (s_done_sema == NULL) {
        captive_portal_tear_down();
        s_prov.run_in_progress = false;
        return ESP_ERR_NO_MEM;
    }

    if (xSemaphoreTake(s_done_sema, portMAX_DELAY) != pdTRUE) {
        ESP_LOGE(TAG, "run: semaphore wait failed");
        captive_portal_tear_down();
        vSemaphoreDelete(s_done_sema);
        s_done_sema = NULL;
        s_prov.run_in_progress = false;
        return ESP_FAIL;
    }

    vSemaphoreDelete(s_done_sema);
    s_done_sema = NULL;

    /* Tear down the httpd. After this, the device keeps the
     * softAP interface alive for a brief moment so the
     * operator's browser still has time to receive the JSON
     * success response before disconnecting. The post-
     * provisioning application body drops the AP if/when it
     * wants to. */
    captive_portal_tear_down();

    s_prov.run_in_progress = false;

    if (!s_last_prov_success) {
        ESP_LOGW(TAG, "run: provisioning ended without success");
        return ESP_FAIL;
    }

    ESP_LOGI(TAG, "run: provisioning complete — credentials persisted to NVS");
    return ESP_OK;
}

void provisioning_stop(void)
{
    ESP_LOGI(TAG, "stop: signal");
    if (s_done_sema) {
        /* success flag stays false → run() returns ESP_FAIL
         * so the caller knows it was force-stopped, not
         * completed via the form. */
        xSemaphoreGive(s_done_sema);
    }
}

esp_err_t provisioning_reset_credentials(void)
{
    ESP_LOGI(TAG, "reset: clearing station config");
    /* esp_wifi_restore() wipes the persisted station config
     * keys in NVS. After this call provisioning_is_provisioned()
     * returns false and the next provisioning_run() brings up
     * the softAP. */
    esp_err_t r = esp_wifi_restore();
    if (r != ESP_OK) {
        ESP_LOGE(TAG, "reset: esp_wifi_restore: %s", esp_err_to_name(r));
        return r;
    }
    ESP_LOGI(TAG, "reset: credentials cleared");
    return ESP_OK;
}

/* Called by the captive /provision handler when the operator's
 * form submission is accepted. Does the wifi config write and
 * signals the semaphore so provisioning_run() resumes. */
esp_err_t provisioning_apply_captive_form(const char *ssid, const char *password)
{
    if (!ssid || !password) return ESP_ERR_INVALID_ARG;
    if (ssid[0] == '\0') return ESP_ERR_INVALID_ARG;

    wifi_config_t wifi_cfg = {0};
    strncpy((char *)wifi_cfg.sta.ssid, ssid, sizeof(wifi_cfg.sta.ssid));
    strncpy((char *)wifi_cfg.sta.password, password,
            sizeof(wifi_cfg.sta.password));
    wifi_cfg.sta.threshold.authmode = WIFI_AUTH_WPA2_PSK;

    esp_err_t r = esp_wifi_set_config(WIFI_IF_STA, &wifi_cfg);
    if (r != ESP_OK) {
        ESP_LOGE(TAG, "apply_form: esp_wifi_set_config: %s",
                 esp_err_to_name(r));
        return r;
    }

    s_last_prov_success = true;
    ESP_LOGI(TAG, "apply_form: ssid=%s password=(redacted) — persisted",
             ssid);

    if (s_done_sema) {
        xSemaphoreGive(s_done_sema);
    }
    return ESP_OK;
}
