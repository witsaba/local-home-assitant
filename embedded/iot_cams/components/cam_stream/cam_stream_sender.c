/* cam_stream_sender.c — W5 TX-mutex-wrapped seam.
 *
 * Every wire write funnels through this one TU:
 *   - the stream task's cam_stream_loop_iteration() (binary
 *     frames per JPEG),
 *   - the WS handshake accept path's hello emit (text frame,
 *     one per session),
 *   - future periodic status frames (text, W7+ status timer).
 *
 * The TX mutex (s_tx_mtx, owned by cam_stream.c) serializes
 * every dispatch so concurrent producers never interleave
 * bytes on the same fd — `xSemaphoreTake(portMAX_DELAY)`
 * since the lock is held for the duration of one
 * httpd_ws_send_frame_async call only.
 *
 * No-nework: pdTRUE vs portTICK_PERIOD_MS policy per
 * esp32-cam-surveillance ws.c:140-142 — portMAX_DELAY for
 * the viewer's own sink call, then return whatever the
 * sink returned. The stream task loops at CAM_STREAM_PERIOD_MS,
 * not blocked on this mutex; the hello emit blocks once at
 * handshake accept; any future status timer fires every 30 s,
 * not contended.
 */

#include <stddef.h>
#include <stdint.h>

#include "freertos/FreeRTOS.h"
#include "freertos/semphr.h"

#include "esp_err.h"

#include "cam_stream.h"

/* TX mutex accessor — exported by cam_stream.c via an
 * internal header (kept module-static to avoid leaking the
 * FreeRTOS handle into the public surface). Declared
 * extern here, defined in cam_stream.c. */
extern SemaphoreHandle_t cam_stream_tx_mtx_get(void);

esp_err_t cam_stream_sink_send_bin(const uint8_t *buf, size_t len)
{
    if (buf == NULL || len == 0) {
        return ESP_ERR_INVALID_ARG;
    }

    SemaphoreHandle_t mtx = cam_stream_tx_mtx_get();
    if (mtx == NULL) {
        /* cam_stream_init was never called or the mutex
         * allocation failed. Refuse the send so the loop
         * body's drop counter ticks instead of crashing. */
        return ESP_ERR_INVALID_STATE;
    }
    if (xSemaphoreTake(mtx, portMAX_DELAY) != pdTRUE) {
        return ESP_ERR_INVALID_STATE;
    }

    esp_err_t r = ESP_ERR_INVALID_STATE;
    const cam_stream_sink_t *sink = cam_stream_sink_get();
    if (sink->is_connected != NULL && sink->is_connected() &&
        sink->send_bin != NULL) {
        r = sink->send_bin(buf, len);
    }

    (void)xSemaphoreGive(mtx);
    return r;
}

esp_err_t cam_stream_sink_send_text(const char *buf, size_t len)
{
    if (buf == NULL || len == 0) {
        return ESP_ERR_INVALID_ARG;
    }

    SemaphoreHandle_t mtx = cam_stream_tx_mtx_get();
    if (mtx == NULL) {
        return ESP_ERR_INVALID_STATE;
    }
    if (xSemaphoreTake(mtx, portMAX_DELAY) != pdTRUE) {
        return ESP_ERR_INVALID_STATE;
    }

    esp_err_t r = ESP_ERR_INVALID_STATE;
    const cam_stream_sink_t *sink = cam_stream_sink_get();
    if (sink->is_connected != NULL && sink->is_connected() &&
        sink->send_text != NULL) {
        r = sink->send_text(buf, len);
    }

    (void)xSemaphoreGive(mtx);
    return r;
}
