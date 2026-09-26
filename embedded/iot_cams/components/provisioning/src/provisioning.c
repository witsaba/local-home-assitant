/* provisioning.c — implementation home of the iot_cams WiFi
 * provisioning component.
 *
 * SCOPE: wraps ESP-IDF v5.5 `wifi_provisioning` (SoftAP scheme,
 * security 1 by default). The bring-up sequence follows the
 * order the IDF softAP example calls out + the IDF v5.5.3
 * ordering requirement that the AP netif must be created BEFORE
 * esp_wifi_init.
 *
 * The bring-up is split across three functions so the same code
 * path is exercised whether provisioning_run() is invoked from a
 * fresh boot or after a factory-reset:
 *
 *   provisioning_run()        — single-threaded master. Composes
 *                               softap_bring_up() with the
 *                               manager start/stop dance.
 *   softap_bring_up()         — idempotent steps that must run
 *                               before wifi_prov_mgr_init().
 *   softap_event_handler()    — friendly log lines for the
 *                               five events that matter during a
 *                               provisioning session (START,
 *                               CRED_RECV, CRED_FAIL,
 *                               CRED_SUCCESS, END).
 *
 * The SoftAP scheme's `service_key` argument is empty: it's only
 * meaningful for the BLE transport where it becomes the BLE GATT
 * passkey. For SoftAP the SSID is `service_name`.
 *
 * The package does not implement the runtime
 * provisioning_config_t::force_provisioning override yet — that
 * 5th piece of state is reserved for a factory-reset flow that
 * the firmware is not yet shipping. When a caller needs forced
 * re-provisioning, they call provisioning_reset_credentials()
 * first and then provisioning_run().
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

#include "wifi_provisioning/manager.h"
#include "wifi_provisioning/scheme_softap.h"

static const char *TAG = "prov";

/* Module-private state. Lives in .bss; initialization is
 * serialized through the boot thread. */
static struct {
    bool                    initialized;
    bool                    manager_running;
    provisioning_security_t security;
    char                    pop[PROV_POP_MAX_LEN + 1];
    char                    service_name[PROV_NAME_MAX_LEN + 1];
    provisioning_app_info_t app_info;
} s_prov;

/* True when the manager has emitted WIFI_PROV_END indicating
 * success. Surfaced on the next call to provisioning_is_provisioned()
 * because the manager already wrote credentials to NVS via
 * esp_wifi_set_config() during the apply_config flow. */
static volatile bool s_prov_end_success = false;

/* Forward decl to keep softap_event_handler() close to its
 * call site in provisioning_run(). */
static void softap_event_handler(void *arg, esp_event_base_t event_base,
                                 int32_t event_id, void *event_data);

/* Forward decl for the iot-cam-info custom protocomm endpoint
 * (T4). The handler signature follows protocomm's
 * `protocomm_req_handler_t`: input is opaque (we ignore it),
 * output is malloc'd and handed back to protocomm which frees it. */
static esp_err_t iot_cam_info_handler(uint32_t session_id,
                                      const uint8_t *inbuf, ssize_t inlen,
                                      uint8_t **outbuf, ssize_t *outlen,
                                      void *priv_data);


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
 * MAC — the canonical Espressif short-form (matches the
 * "ESP_<last-3-MAC>" pattern documented for the softAP bring-up
 * in the IDF provisioning example). */
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
        /* degrade silently to all-zero suffix — still usable */
        memset(mac, 0, sizeof(mac));
    }
    snprintf(out, out_size, "%s_%02X%02X%02X",
             prefix, mac[3], mac[4], mac[5]);
}

/* Custom protocomm endpoint — exposes iot_cams identity to the
 * provisioning client. Registered AFTER start_provisioning per the
 * IDF docs; protocomm frees `*outbuf` after the transport layer
 * hands it to the client. The endpoint is unregistered
 * automatically when the manager stops.
 *
 * Response shape (hand-rolled JSON, defensive): {\"name\":\"...\",
 * \"fw_version\":\"...\"}. Missing fields become empty strings. */
static esp_err_t iot_cam_info_handler(uint32_t session_id,
                                      const uint8_t *inbuf, ssize_t inlen,
                                      uint8_t **outbuf, ssize_t *outlen,
                                      void *priv_data)
{
    (void)session_id;
    (void)inbuf;
    (void)inlen;
    (void)priv_data;

    const char *name = s_prov.app_info.name
        ? s_prov.app_info.name : "";
    const char *fw_version = s_prov.app_info.fw_version
        ? s_prov.app_info.fw_version : "";

    /* Size budget = sum of fixed string length + 2 strings +
     * 8 quotes + null terminator. Hand-rolled to drop the cJSON
     * managed-component dep entirely. */
    const char *tmpl = "{\"name\":\"%s\",\"fw_version\":\"%s\"}";
    size_t need = strlen(tmpl) + strlen(name) + strlen(fw_version) + 1;

    uint8_t *resp = (uint8_t *)malloc(need);
    if (!resp) {
        ESP_LOGE(TAG, "iot-cam-info: out of memory (need %u)",
                 (unsigned)need);
        return ESP_ERR_NO_MEM;
    }
    int n = snprintf((char *)resp, need, tmpl, name, fw_version);
    if (n < 0 || (size_t)n >= need) {
        free(resp);
        return ESP_FAIL;
    }
    *outbuf = resp;
    *outlen = (ssize_t)n;
    return ESP_OK;
}


/* Friendly log lines for the events that matter during a
 * provisioning session. Registered on the default event loop
 * AFTER manager_init(). The WIFI_PROV_END payload is integer
 * encoded (intptr_t) in IDF v5.5; we don't import the
 * wifi_prov_end_reason_t type because it lives in the scheme
 * headers and has shifted across versions. Logging the raw
 * integer is sufficient for post-mortem and stays portable. */
static void softap_event_handler(void *arg, esp_event_base_t event_base,
                                 int32_t event_id, void *event_data)
{
    (void)arg;
    if (event_base != WIFI_PROV_EVENT) return;

    switch (event_id) {
        case WIFI_PROV_START:
            ESP_LOGI(TAG, "Provisioning started");
            break;
        case WIFI_PROV_CRED_RECV: {
            wifi_sta_config_t *wifi_sta_cfg = (wifi_sta_config_t *)event_data;
            /* Avoid printing the password in clear text — the
             * IDF example does it for debugging but we are
             * aiming for a production-grade log surface. */
            ESP_LOGI(TAG, "Received Wi-Fi credentials"
                         "  SSID     : %s"
                         "  Password : (redacted, len=%d)",
                     (const char *)wifi_sta_cfg->ssid,
                     (int)strlen((const char *)wifi_sta_cfg->password));
            break;
        }
        case WIFI_PROV_CRED_FAIL: {
            wifi_prov_sta_fail_reason_t *reason =
                (wifi_prov_sta_fail_reason_t *)event_data;
            ESP_LOGE(TAG, "Provisioning failed"
                          "  Reason : %s"
                          "  Please reset to factory and retry provisioning",
                     (*reason == WIFI_PROV_STA_AUTH_ERROR)
                         ? "Wi-Fi station authentication failed"
                         : "Wi-Fi access-point not found");
            break;
        }
        case WIFI_PROV_CRED_SUCCESS:
            ESP_LOGI(TAG, "Provisioning successful");
            break;
        case WIFI_PROV_END: {
            /* Reason is integer-encoded by the manager. Zero
             * indicates success in IDF v5.5; values > 0 are
             * scheme-specific non-success codes. */
            int reason = (int)(intptr_t)event_data;
            s_prov_end_success = (reason == 0);
            ESP_LOGI(TAG, "Provisioning ended (reason=%d, success=%d)",
                     reason, (int)s_prov_end_success);
            break;
        }
        default:
            break;
    }
}

/* Order-sensitive bring-up. Every step is allowed to be
 * idempotent — esp_netif_init, esp_event_loop_create_default
 * both return ESP_ERR_INVALID_STATE on second call. */
static esp_err_t softap_bring_up(void)
{
    esp_err_t r;

    r = esp_netif_init();
    if (r != ESP_OK && r != ESP_ERR_INVALID_STATE) {
        ESP_LOGE(TAG, "bring_up: esp_netif_init: %s", esp_err_to_name(r));
        return r;
    }

    r = esp_event_loop_create_default();
    if (r != ESP_OK && r != ESP_ERR_INVALID_STATE) {
        ESP_LOGE(TAG, "bring_up: esp_event_loop_create_default: %s",
                 esp_err_to_name(r));
        return r;
    }

    /* AP netif must be created BEFORE esp_wifi_init. IDF v5.5.3
     * returns ESP_ERR_INVALID_STATE if the order is reversed. */
    esp_netif_t *ap_netif = esp_netif_create_default_wifi_ap();
    if (ap_netif == NULL) {
        ESP_LOGE(TAG, "bring_up: default wifi ap netif NULL");
        return ESP_FAIL;
    }
    esp_netif_set_default_netif(ap_netif);

    wifi_init_config_t wifi_init_cfg = WIFI_INIT_CONFIG_DEFAULT();
    r = esp_wifi_init(&wifi_init_cfg);
    if (r != ESP_OK) {
        ESP_LOGE(TAG, "bring_up: esp_wifi_init: %s", esp_err_to_name(r));
        return r;
    }

    /* APSTA — keep the AP for provisioning while preserving the
     * ability to attach to the user's STA after success. The
     * manager drops the AP automatically on WIFI_PROV_END. */
    r = esp_wifi_set_mode(WIFI_MODE_APSTA);
    if (r != ESP_OK) {
        ESP_LOGE(TAG, "bring_up: esp_wifi_set_mode(APSTA): %s",
                 esp_err_to_name(r));
        return r;
    }
    r = esp_wifi_start();
    if (r != ESP_OK) {
        ESP_LOGE(TAG, "bring_up: esp_wifi_start: %s", esp_err_to_name(r));
        return r;
    }

    r = mdns_init();
    if (r != ESP_OK) {
        ESP_LOGE(TAG, "bring_up: mdns_init: %s", esp_err_to_name(r));
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

    /* Make the package truly self-contained: initialize NVS
     * here, before anything in the manager touches storage.
     * Required because esp_wifi_init() opens the wifi NVS
     * namespace and returns ESP_ERR_NVS_NOT_INITIALIZED if we
     * haven't. Idempotent — repeats return ESP_OK or
     * ESP_ERR_INVALID_STATE (e.g. partition already opened). */
    {
        esp_err_t nv = nvs_flash_init();
        if (nv == ESP_ERR_NVS_NO_FREE_PAGES ||
            nv == ESP_ERR_NVS_NEW_VERSION_FOUND) {
            ESP_LOGW(TAG, "init: nvs erase (codes=%s)",
                     esp_err_to_name(nv));
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
    bool provisioned = false;
    /* The manager reads the same NVS storage the wifi driver
     * used to commit ssid+password via esp_wifi_set_config()
     * during the apply_config flow. */
    esp_err_t r = wifi_prov_mgr_is_provisioned(&provisioned);
    if (r != ESP_OK) {
        /* Manager not initialised yet — conservatively report
         * false so the boot branch enters provisioning. */
        return false;
    }
    return provisioned;
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

    /* Fast-path: already provisioned and caller did not force. */
    if (provisioning_is_provisioned()) {
        ESP_LOGI(TAG, "run: already provisioned — skipping softAP bring-up");
        return ESP_OK;
    }

    s_prov.manager_running = true;
    s_prov_end_success = false;

    esp_err_t r = softap_bring_up();
    if (r != ESP_OK) {
        s_prov.manager_running = false;
        return r;
    }

    /* Initialize the manager AFTER bring-up. Both event handlers
     * are zero-init'd → no app-level events besides our own. The
     * SoftAP scheme has no scheme-specific handler macros in IDF
     * v5.5 (only BLE does); pass WIFI_PROV_EVENT_HANDLER_NONE. */
    wifi_prov_mgr_config_t mgr_cfg = {
        .scheme               = wifi_prov_scheme_softap,
        .scheme_event_handler = WIFI_PROV_EVENT_HANDLER_NONE,
    };
    r = wifi_prov_mgr_init(mgr_cfg);
    if (r != ESP_OK) {
        ESP_LOGE(TAG, "run: wifi_prov_mgr_init: %s", esp_err_to_name(r));
        s_prov.manager_running = false;
        return r;
    }

    r = esp_event_handler_register(WIFI_PROV_EVENT,
                                   ESP_EVENT_ANY_ID,
                                   &softap_event_handler, NULL);
    if (r != ESP_OK) {
        ESP_LOGE(TAG, "run: handler_register: %s", esp_err_to_name(r));
        wifi_prov_mgr_deinit();
        s_prov.manager_running = false;
        return r;
    }

    /* Create the iot-cam-info endpoint BEFORE start_provisioning
     * (per the IDF contract). The handler is registered below,
     * AFTER start_provisioning. Endpoint + handler are torn down
     * by the manager when it stops. */
    const char *iot_ep = "iot-cam-info";
    r = wifi_prov_mgr_endpoint_create(iot_ep);
    if (r != ESP_OK) {
        ESP_LOGE(TAG, "run: endpoint_create(%s): %s",
                 iot_ep, esp_err_to_name(r));
        esp_event_handler_unregister(WIFI_PROV_EVENT,
                                     ESP_EVENT_ANY_ID,
                                     &softap_event_handler);
        wifi_prov_mgr_deinit();
        s_prov.manager_running = false;
        return r;
    }

    char ssid[PROV_NAME_MAX_LEN + 1] = {0};
    derive_service_name(ssid, sizeof(ssid));

    /* Security 0 is Kconfig-gated in protocomm. We always try
     * the user's choice; if it's not compiled in, start_provisioning
     * returns ESP_FAIL with a clear log line. */
    wifi_prov_security_t security;
#ifdef CONFIG_ESP_PROTOCOMM_SUPPORT_SECURITY_VERSION_1
    if (s_prov.security == PROV_SECURITY_1) {
        security = WIFI_PROV_SECURITY_1;
    } else
#endif
#ifdef CONFIG_ESP_PROTOCOMM_SUPPORT_SECURITY_VERSION_0
    {
        security = WIFI_PROV_SECURITY_0;
    }
#else
    {
        ESP_LOGE(TAG, "run: no protocomm security versions compiled in");
        esp_event_handler_unregister(WIFI_PROV_EVENT,
                                     ESP_EVENT_ANY_ID,
                                     &softap_event_handler);
        wifi_prov_mgr_deinit();
        s_prov.manager_running = false;
        return ESP_FAIL;
    }
#endif

    ESP_LOGI(TAG, "start_provisioning ssid=%s security=%d",
             ssid, (int)security);
    /* The SoftAP scheme wants service_key = the WPA2 passphrase
     * on the device's softAP network (min 8, max 64 chars).
     * Distinct from the PoP, which authenticates the security-1
     * session that runs OVER the softAP link. */
    const char *softap_pass = CONFIG_PROVISIONING_SOFTAP_PASS;
    r = wifi_prov_mgr_start_provisioning(
        security,
        s_prov.pop,
        ssid,
        softap_pass);
    if (r != ESP_OK) {
        ESP_LOGE(TAG, "run: start_provisioning: %s", esp_err_to_name(r));
        esp_event_handler_unregister(WIFI_PROV_EVENT,
                                     ESP_EVENT_ANY_ID,
                                     &softap_event_handler);
        wifi_prov_mgr_deinit();
        s_prov.manager_running = false;
        return r;
    }

    /* Register the iot-cam-info endpoint handler AFTER
     * start_provisioning (per the IDF contract). The endpoint
     * itself was created before start. */
    r = wifi_prov_mgr_endpoint_register(iot_ep,
                                       iot_cam_info_handler, NULL);
    if (r != ESP_OK) {
        ESP_LOGE(TAG, "run: endpoint_register(%s): %s",
                 iot_ep, esp_err_to_name(r));
        wifi_prov_mgr_stop_provisioning();
        wifi_prov_mgr_wait();
        esp_event_handler_unregister(WIFI_PROV_EVENT,
                                     ESP_EVENT_ANY_ID,
                                     &softap_event_handler);
        wifi_prov_mgr_deinit();
        s_prov.manager_running = false;
        return r;
    }

    /* Block until the manager emits WIFI_PROV_END (success or
     * failure). */
    wifi_prov_mgr_wait();

    esp_event_handler_unregister(WIFI_PROV_EVENT,
                                 ESP_EVENT_ANY_ID,
                                 &softap_event_handler);

    /* Auto-stop already turned off the SoftAP. deinit() releases
     * the protocomm + softAP resources. */
    wifi_prov_mgr_deinit();
    s_prov.manager_running = false;

    if (!s_prov_end_success) {
        ESP_LOGW(TAG, "run: provisioning session ended without success");
        return ESP_FAIL;
    }

    ESP_LOGI(TAG, "run: provisioning complete — credentials persisted to NVS");
    return ESP_OK;
}

void provisioning_stop(void)
{
    if (!s_prov.manager_running) {
        ESP_LOGW(TAG, "stop: nothing to stop");
        return;
    }
    ESP_LOGI(TAG, "stop: calling wifi_prov_mgr_stop_provisioning");
    wifi_prov_mgr_stop_provisioning();
}

esp_err_t provisioning_reset_credentials(void)
{
    ESP_LOGI(TAG, "reset: clearing esp_wifi storage");
    /* The wifi manager stores ssid+password through esp_wifi on
     * apply_config. wifi_prov_mgr_reset_provisioning() wipes
     * those keys from the wifi-managed NVS namespace — the IDF
     * recommended way to clean state for re-provisioning. */
    esp_err_t r = wifi_prov_mgr_reset_provisioning();
    if (r != ESP_OK) {
        ESP_LOGE(TAG, "reset: wifi_prov_mgr_reset_provisioning: %s",
                 esp_err_to_name(r));
        return r;
    }
    ESP_LOGI(TAG, "reset: credentials cleared — next provisioning_run() will start SoftAP");
    return ESP_OK;
}
