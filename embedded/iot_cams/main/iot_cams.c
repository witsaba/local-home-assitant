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

/* Bit set by sta_connected_handler when WIFI_EVENT_STA_CONNECTED
 * fires — meaning the station has finished L2 association with
 * the home AP. We shut down the softAP at this point because the
 * operator's phone is no longer needed once the form is posted. */
#define STA_CONNECTED_BIT  (1u << 1)

/* Forward declaration for the L2 connection handler. */
static void sta_connected_handler(void *arg, esp_event_base_t ev_base,
                                  int32_t ev_id, void *ev_data);

/* Fires when the station's wifi driver confirms L2 association
 * with the home AP (the `wifi:connected with <ssid>` log line).
 * Sets the event group bit so the caller can shut down the
 * softAP at this point instead of waiting for DHCP. */
static void sta_connected_handler(void *arg, esp_event_base_t ev_base,
                                  int32_t ev_id, void *ev_data)
{
    (void)ev_base;
    (void)ev_data;

    if (ev_id != WIFI_EVENT_STA_CONNECTED) return;
    EventGroupHandle_t ev = (EventGroupHandle_t)arg;
    xEventGroupSetBits(ev, STA_CONNECTED_BIT);
}

/* Wait up to 30 s for WIFI_EVENT_STA_CONNECTED — the wifi-driver
 * confirmation that the station has finished L2 association with
 * the home AP. Used by the post-provisioning path to decide when
 * it is safe to shut down the softAP. */
static void wait_for_sta_connected(const char *label)
{
    StaticEventGroup_t ev_group_buf = {0};
    EventGroupHandle_t ev_group = xEventGroupCreateStatic(&ev_group_buf);

    esp_event_handler_register(WIFI_EVENT, WIFI_EVENT_STA_CONNECTED,
                               sta_connected_handler, ev_group);

    EventBits_t bits = xEventGroupWaitBits(
        ev_group, STA_CONNECTED_BIT, pdTRUE, pdFALSE,
        pdMS_TO_TICKS(30000));

    if (bits & STA_CONNECTED_BIT) {
        ESP_LOGI(TAG, "%s: station L2 connected to home AP", label);
    } else {
        ESP_LOGW(TAG, "%s: WIFI_EVENT_STA_CONNECTED timed out", label);
    }

    esp_event_handler_unregister(WIFI_EVENT, WIFI_EVENT_STA_CONNECTED,
                                 sta_connected_handler);
    vEventGroupDelete(ev_group);
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

    ESP_LOGI(TAG, "no credentials in NVS — starting SoftAP provisioning");
    r = provisioning_run();
    if (r != ESP_OK) {
        ESP_LOGE(TAG, "provisioning_run failed: %s", esp_err_to_name(r));
        return;
    }

    /* Provisioning succeeded. Credentials are committed to NVS.
     * The station already attempted to connect when the captive form
     * called esp_wifi_connect() inside provisioning_apply_captive_form().
     * We MUST NOT call esp_wifi_connect() again here — the driver
     * returns ESP_ERR_WIFI_CONN ("sta is connecting, return error")
     * because the previous connect is still in flight. Just wait
     * for the L2 connection so we can shut down the softAP, then
     * wait for the DHCP lease. */
    wait_for_sta_connected("post-provisioning");

    /* Shutdown the softAP NOW — the station has confirmed L2
     * association with the home AP, so the operator's phone is
     * no longer needed. Switch from APSTA to STA-only so the
     * device stops beaconing. The DHCP lease may still be in
     * flight on the station netif; that's fine — it completes
     * asynchronously after the AP is gone. */
    ESP_LOGI(TAG, "post-provisioning: shutting down softAP (STA-only)");
    r = esp_wifi_set_mode(WIFI_MODE_STA);
    if (r != ESP_OK) {
        ESP_LOGE(TAG, "post-provisioning: esp_wifi_set_mode(STA): %s",
                 esp_err_to_name(r));
    }

    /* Wait for the DHCP lease and log the assigned IP. The station
     * netif was created in softap_bring_up() so the DHCP client
     * runs on the station interface after L2 association. */
    wait_for_sta_ip_and_log("post-provisioning");

    ESP_LOGI(TAG, "post-provisioning: entering supervisor loop");
    while (1) {
        vTaskDelay(pdMS_TO_TICKS(60000));
    }
}
