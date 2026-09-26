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

/* Forward decl for the station IP-acquired event handler.
 * Registered in softap_bring_up() so it's armed before any
 * wifi connect attempt; never unregistered (the operator wants
 * to see every home-Wi-Fi join in the log). */
static void sta_got_ip_event_handler(void *arg, esp_event_base_t event_base,
                                    int32_t event_id, void *event_data);

/* Operator-visible "we joined the home AP" log line. Fires on
 * every successful station DHCP lease — the post-provisioning
 * attach, every subsequent reboot that re-joins, and any
 * reconnect after Wi-Fi transient drop. */
static void sta_got_ip_event_handler(void *arg, esp_event_base_t event_base,
                                    int32_t event_id, void *event_data)
{
    (void)arg;
    if (event_base != IP_EVENT || event_id != IP_EVENT_STA_GOT_IP) return;

    ip_event_got_ip_t *event = (ip_event_got_ip_t *)event_data;
    const esp_netif_ip_info_t *ip_info = &event->ip_info;

    /* Filter out IPv6 events — IP_EVENT_STA_GOT_IP fires for
     * both families on dual-stack systems; we only log IPv4
     * so the operator sees a familiar 192.168.x.x address.
     * In ESP-IDF v5.5 esp_netif_ip_info_t stores the IPv4 address
     * as esp_ip4_addr_t (4 bytes); IPv6 variants have the IPv4
     * bytes zeroed. We identify IPv4 by checking that the
     * first byte is non-zero (valid unicast IPv4 always has
     * first byte > 0; all-0 means IPv6-only). */
    uint8_t *b = (uint8_t *)&ip_info->ip.addr;
    if (b[0] == 0 && b[1] == 0 && b[2] == 0 && b[3] == 0) {
        ESP_LOGD(TAG, "sta_got_ip: ignoring IPv6/link-local event");
        return;
    }

    /* Pull the SSID the station just joined so the operator
     * doesn't have to cross-reference with the form they typed.
     * esp_wifi_get_config returns the same NVS-backed storage
     * the form POST wrote through esp_wifi_set_config. */
    wifi_config_t wifi_cfg = {0};
    char ssid[33] = "(unset)";
    if (esp_wifi_get_config(WIFI_IF_STA, &wifi_cfg) == ESP_OK) {
        strncpy(ssid, (const char *)wifi_cfg.sta.ssid, sizeof(ssid));
        ssid[sizeof(ssid) - 1] = '\0';
    }

    ESP_LOGI(TAG, "station connected to \"%s\": ip=" IPSTR
                  " netmask=" IPSTR " gw=" IPSTR,
             ssid,
             IP2STR(&ip_info->ip),
             IP2STR(&ip_info->netmask),
             IP2STR(&ip_info->gw));
}

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

    /* Register the IP_EVENT_STA_GOT_IP handler on the default
     * event loop. Idempotent on repeat bring-ups (which can
     * happen if the device loses creds and re-enters the
     * provisioning branch). */
    esp_event_handler_register(IP_EVENT, IP_EVENT_STA_GOT_IP,
                                sta_got_ip_event_handler, NULL);

    esp_netif_t *ap_netif = esp_netif_create_default_wifi_ap();
    if (ap_netif == NULL) {
        ESP_LOGE(TAG, "softap_bring_up: default wifi ap netif NULL");
        return ESP_FAIL;
    }

    /* CRITICAL: also create the station netif. Without it, the LwIP
     * stack has no DHCP client for the station interface, so
     * IP_EVENT_STA_GOT_IP never fires after the device joins the
     * home AP — confirmed on device (wifi:connected with <ssid>
     * logged but no IP event for 30 s, then timeout). APSTA mode
     * still works at the wifi-driver level for L2 association,
     * but the L3 / DHCP path requires this netif. */
    esp_netif_t *sta_netif = esp_netif_create_default_wifi_sta();
    if (sta_netif == NULL) {
        ESP_LOGE(TAG, "softap_bring_up: default wifi sta netif NULL");
        return ESP_FAIL;
    }

    /* Set the station netif as default — once the softAP is torn
     * down (post-provisioning) the station is the only interface
     * and default routing/hosted services should land there. */
    esp_netif_set_default_netif(sta_netif);

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

    /* Read directly from NVS rather than calling esp_wifi_get_config().
     * The latter returns whatever is in the wifi driver's static
     * s_config[] array, which is only populated after esp_wifi_init()
     * runs (which reads from NVS into RAM). If app_main() calls this
     * function before the wifi stack is initialised, esp_wifi_get_config
     * returns an empty config and we fall into provisioning_run() even
     * though credentials ARE in NVS — confirmed on device after a
     * power cycle.
     *
     * The IDF wifi driver stores its config under namespace
     * "nvs.net80211" with SEPARATE keys per field:
     *   - sta.ssid     (blob, up to 32 bytes)
     *   - sta.pswd     (blob, up to 64 bytes)
     *   - sta.bssid    etc.
     * It is NOT a single "config" blob. Verified against the
     * wifi_nvs_config example in IDF v5.5.x. */
    nvs_handle_t nvs;
    esp_err_t err = nvs_open("nvs.net80211", NVS_READONLY, &nvs);
    if (err != ESP_OK) {
        /* Namespace missing or NVS not initialised — treat as
         * not provisioned. The first-boot flow will create the
         * namespace when provisioning_apply_captive_form() writes
         * the credentials. */
        return false;
    }

    uint8_t ssid[32] = {0};
    size_t ssid_len = sizeof(ssid);
    err = nvs_get_blob(nvs, "sta.ssid", ssid, &ssid_len);
    nvs_close(nvs);

    if (err != ESP_OK) {
        /* Key not found or read error — no credentials yet. */
        return false;
    }
    /* An SSID of all zeros means the wifi driver wrote an empty
     * config (first-boot state). Any non-zero first byte means a
     * real SSID was persisted. */
    return ssid[0] != '\0';
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

esp_err_t provisioning_join_ap(void)
{
    if (!s_prov.initialized) {
        ESP_LOGE(TAG, "join_ap: not initialized");
        return ESP_ERR_INVALID_STATE;
    }
    if (!provisioning_is_provisioned()) {
        ESP_LOGE(TAG, "join_ap: no credentials in NVS");
        return ESP_ERR_INVALID_STATE;
    }

    esp_err_t r;

    /* Idempotent netif + event loop init. ESP_ERR_INVALID_STATE
     * is acceptable — means already initialized. */
    r = esp_netif_init();
    if (r != ESP_OK && r != ESP_ERR_INVALID_STATE) {
        ESP_LOGE(TAG, "join_ap: esp_netif_init: %s", esp_err_to_name(r));
        return r;
    }
    r = esp_event_loop_create_default();
    if (r != ESP_OK && r != ESP_ERR_INVALID_STATE) {
        ESP_LOGE(TAG, "join_ap: esp_event_loop_create_default: %s",
                 esp_err_to_name(r));
        return r;
    }

    /* Register the permanent IP event handler so the
     * `station connected to "<ssid>": ip=...` log fires on every
     * subsequent attach (reboot, transient reconnect). Idempotent
     * on repeat bring-ups. */
    esp_event_handler_register(IP_EVENT, IP_EVENT_STA_GOT_IP,
                                sta_got_ip_event_handler, NULL);

    /* Station netif (not AP — we are STA-only here). */
    esp_netif_t *sta_netif = esp_netif_create_default_wifi_sta();
    if (sta_netif == NULL) {
        ESP_LOGE(TAG, "join_ap: create_default_wifi_sta returned NULL");
        return ESP_FAIL;
    }
    esp_netif_set_default_netif(sta_netif);

    wifi_init_config_t wifi_init_cfg = WIFI_INIT_CONFIG_DEFAULT();
    r = esp_wifi_init(&wifi_init_cfg);
    if (r != ESP_OK && r != ESP_ERR_INVALID_STATE) {
        ESP_LOGE(TAG, "join_ap: esp_wifi_init: %s", esp_err_to_name(r));
        return r;
    }

    /* STA-only mode — no softAP. Used on already-provisioned
     * boots where the operator never interacts with the device. */
    r = esp_wifi_set_mode(WIFI_MODE_STA);
    if (r != ESP_OK) {
        ESP_LOGE(TAG, "join_ap: esp_wifi_set_mode(STA): %s",
                 esp_err_to_name(r));
        return r;
    }
    r = esp_wifi_start();
    if (r != ESP_OK) {
        ESP_LOGE(TAG, "join_ap: esp_wifi_start: %s", esp_err_to_name(r));
        return r;
    }

    /* Trigger the station attach. The credentials are already in
     * NVS (we checked above), so the driver will read them and
     * associate with the home AP. ESP_ERR_WIFI_CONN means the
     * driver is already mid-connect (e.g., from a previous
     * provisioning_apply_captive_form() call) — treat as success. */
    r = esp_wifi_connect();
    if (r != ESP_OK && r != ESP_ERR_WIFI_CONN) {
        ESP_LOGE(TAG, "join_ap: esp_wifi_connect: %s",
                 esp_err_to_name(r));
        return r;
    }

    ESP_LOGI(TAG, "join_ap: station connecting (credentials from NVS)");
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

    /* Kick off the station connect. In APSTA mode this only
     * affects the STA side; the softAP stays up so the
     * operator's browser gets the JSON success response before
     * they disconnect. The IP_EVENT_STA_GOT_IP handler logs the
     * assigned address. */
    esp_err_t cr = esp_wifi_connect();
    if (cr != ESP_OK && cr != ESP_ERR_WIFI_CONN) {
        /* ESP_ERR_WIFI_CONN means the STA is already busy;
         * the connect call still queued. Treat as success since
         * the config IS in NVS and the wifi driver will retry. */
        ESP_LOGE(TAG, "apply_form: esp_wifi_connect: %s",
                 esp_err_to_name(cr));
        return cr;
    }

    s_last_prov_success = true;
    ESP_LOGI(TAG, "apply_form: ssid=%s password=(redacted) — persisted",
             ssid);

    if (s_done_sema) {
        xSemaphoreGive(s_done_sema);
    }
    return ESP_OK;
}
