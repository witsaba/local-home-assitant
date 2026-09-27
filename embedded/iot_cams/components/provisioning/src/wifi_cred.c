/* wifi_cred.c — manual WiFi credential persistence.
 *
 * Mirrors the esp32-cam-surveillance config.c pattern: the wifi
 * driver's esp_wifi_set_config() internal NVS storage ("nvs.net80211")
 * is NOT relied upon for persistence. Instead, we write credentials
 * to our own NVS namespace "wifi_cred" as explicit string keys
 * "ssid" and "password" and read them back on boot.
 *
 * This is the only path that survived the power-cycle test on the
 * real device. The wifi driver's internal storage under
 * "nvs.net80211" was not surviving reboots reliably.
 */
#include <string.h>

#include "esp_log.h"
#include "nvs.h"

static const char *TAG = "wifi_cred";
static const char *NS = "wifi_cred";

esp_err_t wifi_cred_save(const char *ssid, const char *password)
{
    nvs_handle_t h;
    esp_err_t err = nvs_open(NS, NVS_READWRITE, &h);
    if (err != ESP_OK) {
        ESP_LOGE(TAG, "save: nvs_open: %s", esp_err_to_name(err));
        return err;
    }

    /* Erase empty values to keep namespace minimal. */
    if (ssid && ssid[0] != '\0') {
        err = nvs_set_str(h, "ssid", ssid);
        if (err != ESP_OK) {
            ESP_LOGE(TAG, "save: nvs_set_str(ssid): %s", esp_err_to_name(err));
            nvs_close(h);
            return err;
        }
    } else {
        nvs_erase_key(h, "ssid");
    }

    if (password && password[0] != '\0') {
        err = nvs_set_str(h, "password", password);
        if (err != ESP_OK) {
            ESP_LOGE(TAG, "save: nvs_set_str(password): %s", esp_err_to_name(err));
            nvs_close(h);
            return err;
        }
    } else {
        nvs_erase_key(h, "password");
    }

    err = nvs_commit(h);
    nvs_close(h);
    if (err != ESP_OK) {
        ESP_LOGE(TAG, "save: nvs_commit: %s", esp_err_to_name(err));
        return err;
    }

    ESP_LOGI(TAG, "save: ssid='%s' saved to NVS namespace '%s'", ssid, NS);
    return ESP_OK;
}

esp_err_t wifi_cred_load(char *out_ssid, size_t ssid_size,
                         char *out_password, size_t password_size)
{
    nvs_handle_t h;
    esp_err_t err = nvs_open(NS, NVS_READONLY, &h);
    if (err == ESP_ERR_NVS_NOT_FOUND) {
        /* Namespace doesn't exist — no credentials ever saved. */
        if (out_ssid)     out_ssid[0]     = '\0';
        if (out_password) out_password[0] = '\0';
        return ESP_ERR_NOT_FOUND;
    }
    if (err != ESP_OK) {
        ESP_LOGE(TAG, "load: nvs_open: %s", esp_err_to_name(err));
        return err;
    }

    size_t len;

    if (out_ssid) {
        out_ssid[0] = '\0';
        len = ssid_size;
        err = nvs_get_str(h, "ssid", out_ssid, &len);
        if (err != ESP_OK && err != ESP_ERR_NVS_NOT_FOUND) {
            ESP_LOGE(TAG, "load: nvs_get_str(ssid): %s", esp_err_to_name(err));
            nvs_close(h);
            return err;
        }
    }

    if (out_password) {
        out_password[0] = '\0';
        len = password_size;
        err = nvs_get_str(h, "password", out_password, &len);
        if (err != ESP_OK && err != ESP_ERR_NVS_NOT_FOUND) {
            ESP_LOGE(TAG, "load: nvs_get_str(password): %s", esp_err_to_name(err));
            nvs_close(h);
            return err;
        }
    }

    nvs_close(h);
    return ESP_OK;
}

bool wifi_cred_is_saved(void)
{
    char ssid[33] = {0};
    size_t len = sizeof(ssid);
    nvs_handle_t h;
    esp_err_t err = nvs_open(NS, NVS_READONLY, &h);
    if (err != ESP_OK) {
        return false;
    }
    err = nvs_get_str(h, "ssid", ssid, &len);
    nvs_close(h);
    return err == ESP_OK && ssid[0] != '\0';
}
