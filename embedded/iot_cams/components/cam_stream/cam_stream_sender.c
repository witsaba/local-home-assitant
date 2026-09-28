/* cam_stream_sender.c — single seam through which the stream
 * loop pushes JPEG bytes (and W3+ future hello/status builders
 * push text frames) into the installed viewer sink.
 *
 * Today the only caller is `cam_stream_loop_iteration` inside
 * `cam_stream.c`. Centralizing the call here means W5 only
 * needs to wrap a TX mutex around this one TU to serialize
 * every wire write — the stream loop, the hello frame at
 * handshake accept, and the periodic status timer all funnel
 * through the same take/give without touching the loop body.
 *
 * The function always honors the no-viewer failure path
 * (ESP_ERR_INVALID_STATE) so the loop body's drop-counter
 * path stays trivial.
 */

#include <stddef.h>
#include <stdint.h>

#include "cam_stream.h"

esp_err_t cam_stream_sink_send_bin(const uint8_t *buf, size_t len)
{
    if (buf == NULL || len == 0) {
        return ESP_ERR_INVALID_ARG;
    }
    const cam_stream_sink_t *sink = cam_stream_sink_get();
    if (sink->is_connected == NULL || !sink->is_connected()) {
        return ESP_ERR_INVALID_STATE;
    }
    if (sink->send_bin == NULL) {
        return ESP_ERR_INVALID_STATE;
    }
    return sink->send_bin(buf, len);
}

esp_err_t cam_stream_sink_send_text(const char *buf, size_t len)
{
    if (buf == NULL || len == 0) {
        return ESP_ERR_INVALID_ARG;
    }
    const cam_stream_sink_t *sink = cam_stream_sink_get();
    if (sink->is_connected == NULL || !sink->is_connected()) {
        return ESP_ERR_INVALID_STATE;
    }
    if (sink->send_text == NULL) {
        return ESP_ERR_INVALID_STATE;
    }
    return sink->send_text(buf, len);
}
