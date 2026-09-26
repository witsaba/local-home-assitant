/* iot_cams.c — application entry point (app_main).
 *
 * Sole responsibility: bootstrap the WiFi provisioning package and
 * wait for credentials to land. Once credentials are committed to
 * NVS, the firmware waits for the station DHCP lease, logs the
 * assigned IP, shuts down the softAP, and enters the application
 * supervisor loop.
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
#include <string.h>

#include "esp_log.h"
#include "esp_netif.h"
#include "esp_wifi.h"
#include "esp_event.h"
#include "esp_mac.h"

#include "provisioning.h"

static const char *TAG = "app_main";

static const char *FW_VERSION = "0.1.0";

/* Bit set by post_prov_ip_handler when IP_EVENT_STA_GOT_IP fires. */
#define GOT_IP_BIT  (1u << 0)

/* Forward declaration — defined after app_main(). */
static void post_prov_ip_handler(void *arg, esp_event_base_t ev_base,
                                 int32_t ev_id, void *ev_data);

/* One-shot IP handler for the post-provisioning block. Fires once
 * when the station gets its first DHCP lease from the home AP,
 * extracts the IP/netmask/gw, logs it, and signals the waiting
 * task via the event group so the supervisor loop can continue. */
static void post_prov_ip_handler(void *arg, esp_event_base_t ev_base,
                                 int32_t ev_id, void *ev_data)
{
    (void)ev_base;

    if (ev_id != IP_EVENT_STA_GOT_IP) return;

    EventGroupHandle_t ev = (EventGroupHandle_t)arg;
    ip_event_got_ip_t *event = (ip_event_got_ip_t *)ev_data;
    const esp_netif_ip_info_t *ip = &event->ip_info;

    /* Filter out IPv6 events — we only want the IPv4 DHCP lease
     * so the operator sees a familiar 192.168.x.x address. */
    uint8_t *b = (uint8_t *)&ip->ip.addr;
    if (b[0] == 0 && b[1] == 0 && b[2] == 0 && b[3] == 0) return;

    /* Pull the SSID so the log is self-contained. */
    wifi_config_t wcfg = {0};
    char ssid[33] = "(unknown)";
    if (esp_wifi_get_config(WIFI_IF_STA, &wcfg) == ESP_OK) {
        strncpy(ssid, (const char *)wcfg.sta.ssid, sizeof(ssid) - 1);
        ssid[sizeof(ssid) - 1] = '\0';
    }

    ESP_LOGI(TAG, "station connected to \"%s\": ip=" IPSTR
                  " netmask=" IPSTR " gw=" IPSTR,
             ssid,
             IP2STR(&ip->ip),
             IP2STR(&ip->netmask),
             IP2STR(&ip->gw));

    xEventGroupSetBits(ev, GOT_IP_BIT);
}

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
     *
     * The station already attempted to connect when the captive form
     * called esp_wifi_connect() inside provisioning_apply_captive_form().
     * It may have connected already (fastpath) or is still in progress
     * (DHCP pending). Either way, we now:
     *
     *   1. Ensure the station netif is alive.
     *   2. Wait up to 15 s for IP_EVENT_STA_GOT_IP.
     *   3. Log the assigned IP.
     *   4. Shut down the softAP (STA-only).
     *   5. Enter the supervisor loop.
     *
     * The wifi driver and event loop run on their own tasks;
     * we just need this task alive so esp_event_loop_run() keeps
     * dispatching events to the handlers registered in softap_bring_up().
     */

    /* Make sure the station netif is attached to the event loop. */
    esp_netif_t *sta_netif = esp_netif_get_default_netif();
    if (!sta_netif) {
        ESP_LOGW(TAG, "post-provisioning: no default netif — creating sta");
        sta_netif = esp_netif_create_default_wifi_sta();
    }

    /* Re-issue esp_wifi_connect() in case the previous call from
     * provisioning_apply_captive_form() raced with teardown or the
     * driver decided to defer the connect. ESP_ERR_WIFI_CONN means
     * the station is already mid-connect (from the form's call) —
     * not an error; the driver will keep retrying. */
    ESP_LOGI(TAG, "post-provisioning: triggering station connect");
    r = esp_wifi_connect();
    if (r != ESP_OK && r != ESP_ERR_WIFI_CONN) {
        ESP_LOGW(TAG, "post-provisioning: esp_wifi_connect: %s",
                 esp_err_to_name(r));
    }

    /* Register a blocking-wait handler for IP_EVENT_STA_GOT_IP.
     * We create a temporary event group so the handler can signal
     * this task. It auto-deregisters after the event fires or the
     * timeout expires — whichever comes first. */
    StaticEventGroup_t ev_group_buf = {0};
    EventGroupHandle_t ev_group = xEventGroupCreateStatic(&ev_group_buf);

    esp_event_handler_register(IP_EVENT, IP_EVENT_STA_GOT_IP,
                               post_prov_ip_handler, ev_group);

    /* Wait up to 30 seconds for the DHCP lease. Home routers vary
     * widely; 15 s was too tight for a slow DHCP responder. The
     * softAP is still beaconing during this window so the phone's
     * connection is unaffected. */
    EventBits_t bits = xEventGroupWaitBits(
        ev_group, GOT_IP_BIT, pdTRUE, pdFALSE,
        pdMS_TO_TICKS(30000));

    if (bits & GOT_IP_BIT) {
        ESP_LOGI(TAG, "post-provisioning: station DHCP lease acquired");
    } else {
        ESP_LOGW(TAG, "post-provisioning: IP_EVENT_STA_GOT_IP timed out "
                      "(DHCP may still complete in the background)");
    }

    /* Deregister so the handler does not fire again on reconnects.
     * The permanent handler from softap_bring_up() is still active
     * for every subsequent attach. */
    esp_event_handler_unregister(IP_EVENT, IP_EVENT_STA_GOT_IP,
                                post_prov_ip_handler);

    vEventGroupDelete(ev_group);

    /* Shutdown the softAP. The operator's phone was already
     * disconnected when provisioning_run() called captive_portal_tear_down()
     * (httpd_stop). Now we go further: switch from APSTA to STA-only
     * so the device stops beaconing as an AP entirely. It is now
     * a pure client on the provisioned network. */
    ESP_LOGI(TAG, "post-provisioning: shutting down softAP (STA-only)");
    r = esp_wifi_set_mode(WIFI_MODE_STA);
    if (r != ESP_OK) {
        ESP_LOGE(TAG, "post-provisioning: esp_wifi_set_mode(STA): %s",
                 esp_err_to_name(r));
    }

    ESP_LOGI(TAG, "post-provisioning: entering supervisor loop");
    while (1) {
        vTaskDelay(pdMS_TO_TICKS(60000));
    }
}
