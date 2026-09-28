/* cam_reader.c — iot_cams OV2640 bring-up + serialized capture API.
 *
 * Ported from
 *     ../rural_home_assistant/backend/iot-camera/components/cam_reader/cam_reader.c
 *
 * The pin map, sensor defaults (SVGA 800x600, JPEG quality 12,
 * `grab_mode = CAMERA_GRAB_LATEST`), PSRAM-driven `fb_count` /
 * `fb_location` logic, and `init_camera()` body are unchanged —
 * the iot_cams firmware targets the same AI-Thinker ESP32-CAM
 * board family, so the AI-Thinker pin map (PWDN=32, XCLK=0,
 * SIOD=26, SIOC=27, D7..D0={35,34,39,36,21,19,18,5},
 * VSYNC=25, HREF=23, PCLK=22) is reusable byte-for-byte.
 *
 * Difference from the reference: iot_cams lifts the binary
 * semaphore into this component so it can guard every future
 * consumer (httpd handlers, future FreeRTOS task / WebSocket /
 * MQTT paths). The reference keeps the semaphore inside
 * `http_server.c` because its only consumer is the HTTP server.
 */

#include <stddef.h>

#include "freertos/FreeRTOS.h"
#include "freertos/semphr.h"

#include "esp_log.h"
#include "esp_psram.h"
#include "esp_camera.h"

#include "cam_reader.h"

static const char *TAG = "cam-reader";

/* AI-Thinker ESP32-CAM board pin map. Identical to the reference. */
#define CAM_PIN_PWDN    32
#define CAM_PIN_RESET   -1   /* software reset; no physical pin */
#define CAM_PIN_XCLK    0
#define CAM_PIN_SIOD    26
#define CAM_PIN_SIOC    27

#define CAM_PIN_D7      35
#define CAM_PIN_D6      34
#define CAM_PIN_D5      39
#define CAM_PIN_D4      36
#define CAM_PIN_D3      21
#define CAM_PIN_D2      19
#define CAM_PIN_D1      18
#define CAM_PIN_D0       5
#define CAM_PIN_VSYNC   25
#define CAM_PIN_HREF    23
#define CAM_PIN_PCLK    22

#define CAM_XCLK_FREQ_HZ   20000000   /* 20 MHz */
#define CAM_JPEG_QUALITY   12         /* lower == higher quality */
#define CAM_FB_COUNT_PSRAM 2
#define CAM_FB_COUNT_NOMEM 1

/* Mutex wait budget for a single capture. The reference uses
 * 5 s; we mirror that as an executable contract. */
#define CAM_READER_WAIT_MS 5000

/* Single binary semaphore, created in cam_reader_init() and
 * given once to mark the unlocked state. NULL until init. */
static SemaphoreHandle_t s_cam_mutex = NULL;

esp_err_t cam_reader_init(void)
{
    if (s_cam_mutex != NULL) {
        ESP_LOGW(TAG, "init: already initialized");
        return ESP_ERR_INVALID_STATE;
    }

    s_cam_mutex = xSemaphoreCreateBinary();
    if (s_cam_mutex == NULL) {
        ESP_LOGE(TAG, "init: xSemaphoreCreateBinary failed");
        return ESP_ERR_NO_MEM;
    }
    xSemaphoreGive(s_cam_mutex);

    /* PSRAM detection drives the buffer config at runtime. */
    size_t psram_size = esp_psram_get_size();

    camera_config_t camera_config = {
        .pin_pwdn      = CAM_PIN_PWDN,
        .pin_reset     = CAM_PIN_RESET,
        .pin_xclk      = CAM_PIN_XCLK,
        .pin_sccb_sda  = CAM_PIN_SIOD,
        .pin_sccb_scl  = CAM_PIN_SIOC,

        .pin_d7        = CAM_PIN_D7,
        .pin_d6        = CAM_PIN_D6,
        .pin_d5        = CAM_PIN_D5,
        .pin_d4        = CAM_PIN_D4,
        .pin_d3        = CAM_PIN_D3,
        .pin_d2        = CAM_PIN_D2,
        .pin_d1        = CAM_PIN_D1,
        .pin_d0        = CAM_PIN_D0,
        .pin_vsync     = CAM_PIN_VSYNC,
        .pin_href      = CAM_PIN_HREF,
        .pin_pclk      = CAM_PIN_PCLK,

        .xclk_freq_hz  = CAM_XCLK_FREQ_HZ,
        .ledc_timer    = LEDC_TIMER_0,
        .ledc_channel  = LEDC_CHANNEL_0,

        .pixel_format  = PIXFORMAT_JPEG,
        .frame_size    = FRAMESIZE_SVGA,   /* 800x600 */
        .jpeg_quality  = CAM_JPEG_QUALITY,

        .fb_count      = (psram_size > 0) ? CAM_FB_COUNT_PSRAM
                                           : CAM_FB_COUNT_NOMEM,
        .fb_location   = (psram_size > 0) ? CAMERA_FB_IN_PSRAM
                                           : CAMERA_FB_IN_DRAM,
        .grab_mode     = CAMERA_GRAB_LATEST,
    };

    ESP_LOGI(TAG, "init: PSRAM=%zu bytes, fb_count=%d",
             psram_size, camera_config.fb_count);

    esp_err_t err = esp_camera_init(&camera_config);
    if (err != ESP_OK) {
        ESP_LOGE(TAG, "init: esp_camera_init: %s", esp_err_to_name(err));
        vSemaphoreDelete(s_cam_mutex);
        s_cam_mutex = NULL;
        return err;
    }

    ESP_LOGI(TAG, "init: camera ready (SVGA 800x600, JPEG quality %d)",
             CAM_JPEG_QUALITY);
    return ESP_OK;
}

esp_err_t cam_reader_capture(camera_fb_t **fb)
{
    if (fb == NULL) {
        return ESP_ERR_INVALID_ARG;
    }
    if (s_cam_mutex == NULL) {
        return ESP_ERR_INVALID_STATE;
    }
    *fb = NULL;

    /* 5 s wait budget, mirrors the reference project. */
    if (xSemaphoreTake(s_cam_mutex, pdMS_TO_TICKS(CAM_READER_WAIT_MS)) != pdTRUE) {
        ESP_LOGW(TAG, "capture: mutex timeout (>%d ms) — caller is busy",
                 CAM_READER_WAIT_MS);
        return ESP_ERR_TIMEOUT;
    }

    /* esp_timer_get_time() is a microsecond counter; convert to ms. */
    uint64_t start_us = esp_timer_get_time();
    camera_fb_t *out = esp_camera_fb_get();
    uint64_t elapsed_ms = (esp_timer_get_time() - start_us) / 1000ULL;

    if (out == NULL) {
        ESP_LOGE(TAG, "capture: esp_camera_fb_get failed");
        /* The mutex has been taken; the caller MUST call
         * cam_reader_release(NULL) to drop it. */
        return ESP_FAIL;
    }

    ESP_LOGD(TAG, "capture: %zu bytes in %llu ms",
             out->len, (unsigned long long)elapsed_ms);

    *fb = out;
    return ESP_OK;
}

void cam_reader_release(camera_fb_t *fb)
{
    if (fb != NULL) {
        esp_camera_fb_return(fb);
    }
    if (s_cam_mutex != NULL) {
        xSemaphoreGive(s_cam_mutex);
    }
}
