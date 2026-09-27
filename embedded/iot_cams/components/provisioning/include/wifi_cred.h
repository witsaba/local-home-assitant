/* wifi_cred.h — manual WiFi credential persistence.
 *
 * Saves WiFi ssid + password to NVS namespace "wifi_cred".
 * This is the only persistence path that survived the power-cycle
 * test on the real ESP32 device.
 */
#ifndef WIFI_CRED_H
#define WIFI_CRED_H

#include <stddef.h>
#include <stdbool.h>
#include "esp_err.h"

/* Save ssid + password to NVS namespace "wifi_cred".
 * Empty ssid → key erased. Empty password → key erased.
 * Returns ESP_OK on success. */
esp_err_t wifi_cred_save(const char *ssid, const char *password);

/* Load ssid + password from NVS. Sets output buffers to empty
 * string if key is absent. Returns ESP_OK if at least ssid is
 * present, ESP_ERR_NOT_FOUND if namespace/key is missing. */
esp_err_t wifi_cred_load(char *out_ssid, size_t ssid_size,
                          char *out_password, size_t password_size);

/* True iff ssid key is present and non-empty in "wifi_cred". */
bool wifi_cred_is_saved(void);

#endif /* WIFI_CRED_H */
