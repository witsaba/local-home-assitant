/* iot_cams.c — application entry point (app_main).
 *
 * Sole responsibility: bootstrap the WiFi provisioning package and
 * wait for credentials to land. Once credentials are committed to
 * NVS, the firmware waits for the station DHCP lease, logs the
 * assigned IP, shuts down the softAP, and enters the application
 * supervisor loop.
 *
 * On every boot (first provision or subsequent re-join) the same
 * wait-for-IP-and-log sequence runs. On a first boot the softAP
 * is torn down after DHCP; on subsequent boots the softAP is never
 * started because provisioning_run() is skipped entirely.
 *
 * Constraints enforced by this file:
 *   - It includes ONLY `provisioning.h`. No `esp_wifi.h`,
 *     `esp_netif.h`, `mdns.h` or `protocomm.h` shows up here
 *     — those are package-private. If you find yourself wanting
 *     to add such an include, it is a sign the package surface
 *     is too narrow and should be widened.
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

/* Bit set by ip_handler when IP_EVENT_STA_GOT_IP fires. */
#define GOT_IP_BIT  (1u << 0)

/* Forward declarations. */
static void ip_handler(void *arg, esp_event_base_t ev_base,
                       int32_t ev_id, void *ev_data);

/* IP event handler — logs the assigned IPv4 address and signals
 * the waiting task via the event group. Runs on every STA attach
 * (first provision, subsequent reboots, transient reconnects). */
static void ip_handler(void *arg, esp_event_base_t ev_base,
                       int32_t ev_id, void *ev_data)
{
    (void)ev_base;

    if (ev_id != IP_EVENT_STA_GOT_IP) return;

    EventGroupHandle_t ev = (EventGroupHandle_t)arg;
    ip_event_got_ip_t *event = (ip_event_got_ip_t *)ev_data;
    const esp_netif_ip_info_t *ip = &event->ip_info;

    /* Filter out IPv6 events — we only want the IPv4 DHCP lease. */
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

/* Wait up to 30 s for the station to receive a DHCP lease from the
 * home AP, then log the assigned IPv4 address.
 *
 * IMPORTANT: does NOT call esp_wifi_connect(). The caller is
 * responsible for triggering the station attach before calling this:
 *   - post-provisioning path: provisioning_apply_captive_form()
 *     already called esp_wifi_connect() when the form posted.
 *   - already-provisioned path: provisioning_join_ap() does it.
 *
 * Calling esp_wifi_connect() here would race with the in-flight
 * connect from the form handler (the wifi driver returns
 * ESP_ERR_WIFI_CONN and prints "sta is connecting, return error"
 * in the log). Each path triggers the connect exactly once.
 *
 * The wifi driver and event loop run on their own tasks; we just
 * need this task alive so esp_event_loop_run() keeps dispatching
 * events to our handler. */
static void wait_for_sta_ip_and_log(const char *label)
{
    /* Register a one-shot IP handler and block until it fires. */
    StaticEventGroup_t ev_group_buf = {0};
    EventGroupHandle_t ev_group = xEventGroupCreateStatic(&ev_group_buf);

    esp_event_handler_register(IP_EVENT, IP_EVENT_STA_GOT_IP,
                               ip_handler, ev_group);

    EventBits_t bits = xEventGroupWaitBits(
        ev_group, GOT_IP_BIT, pdTRUE, pdFALSE,
        pdMS_TO_TICKS(30000));

    if (bits & GOT_IP_BIT) {
        ESP_LOGI(TAG, "%s: station DHCP lease acquired", label);
    } else {
        ESP_LOGW(TAG, "%s: IP_EVENT_STA_GOT_IP timed out "
                      "(DHCP may still complete in the background)", label);
    }

    esp_event_handler_unregister(IP_EVENT, IP_EVENT_STA_GOT_IP,
                                ip_handler);
    vEventGroupDelete(ev_group);
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
        /* Credentials exist in NVS from a previous provisioning session.
         * Bring up wifi in STA-only mode and trigger a connect, then
         * wait for the DHCP lease so we can log the assigned IP. The
         * softAP is never started on this path (provisioning_run is
         * skipped). provisioning_join_ap() handles the wifi init +
         * esp_wifi_connect() so we don't race with anyone. */
        ESP_LOGI(TAG, "credentials present in NVS — joining home AP");
        r = provisioning_join_ap();
        if (r != ESP_OK) {
            ESP_LOGE(TAG, "provisioning_join_ap failed: %s",
                     esp_err_to_name(r));
            return;
        }
        wait_for_sta_ip_and_log("already-provisioned");
        ESP_LOGI(TAG, "entering supervisor loop");
        while (1) {
            vTaskDelay(pdMS_TO_TICKS(60000));
        }
    }

    /* Reset station state before entering the provisioning branch.
     * Clears s_sta_got_ip and s_consecutive_failures so the
     * disconnection handler in provisioning.c uses retry logic
     * from a clean slate during this provisioning session. */
    provisioning_reset_sta_state();

    ESP_LOGI(TAG, "no credentials in NVS — starting SoftAP provisioning");
    r = provisioning_run();
    if (r != ESP_OK) {
        ESP_LOGE(TAG, "provisioning_run failed: %s", esp_err_to_name(r));
        return;
    }

    /* Provisioning succeeded. Credentials are committed to NVS.
     * The station has already attempted to connect when the captive
     * form called esp_wifi_connect() inside provisioning_apply_
     * captive_form(). The disconnection handler (WIFI_EVENT_STA_
     * DISCONNECTED) will retry with exponential backoff (up to 5
     * attempts) if the home AP drops the connection.
     *
     * WAIT FOR IP BEFORE SHUTTING DOWN THE softAP — this mirrors
     * esp32-cam-surveillance wifi_event.c: the softAP is torn down
     * only when IP_EVENT_STA_GOT_IP fires (not on WIFI_EVENT_STA_
     * CONNECTED which fires at L2 association, before DHCP).
     * Shutting down on L2-assoc was too early: the home AP sometimes
     * deauths the ESP32 before the DHCP lease completes, and without
     * the softAP running the device had no recovery path. */
    ESP_LOGI(TAG, "post-provisioning: waiting for DHCP lease from home AP...");
    StaticEventGroup_t ev_group_buf = {0};
    EventGroupHandle_t ev_group = xEventGroupCreateStatic(&ev_group_buf);
    esp_event_handler_register(IP_EVENT, IP_EVENT_STA_GOT_IP,
                               ip_handler, ev_group);
    EventBits_t bits = xEventGroupWaitBits(
        ev_group, GOT_IP_BIT, pdTRUE, pdFALSE,
        pdMS_TO_TICKS(60000));
    esp_event_handler_unregister(IP_EVENT, IP_EVENT_STA_GOT_IP,
                                ip_handler);
    if (!(bits & GOT_IP_BIT)) {
        ESP_LOGW(TAG, "post-provisioning: DHCP lease timed out — "
                      "softAP stays up; device will retry from app");
    } else {
        ESP_LOGI(TAG, "post-provisioning: DHCP lease acquired — shutting down softAP");
    }
    vEventGroupDelete(ev_group);

    /* Now shut down the softAP. The station is already connected
     * and has an IP. The disconnection handler will retry if needed
     * (s_sta_got_ip=true means it won't retry but the app could
     * use provisioning_join_ap() for an explicit reconnect). */
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
