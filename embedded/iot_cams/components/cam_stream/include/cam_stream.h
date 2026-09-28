/* cam_stream.h — public API for the iot_cams camera-stream
 * consumer component (FW-15/FW-16 style port, scoped for the
 * /ws/cams endpoint plan in `odd/tasks/ws-cams-endpoint.md`).
 *
 * Long-lived FreeRTOS task that consumes JPEG frames from
 * `cam_reader` in a loop and ships them to a `cam_stream_sink_t`
 * function-pointer seam installed by the provisioning component
 * at /ws/cams handshake accept time.
 *
 * The public surface is intentionally narrow: it does not expose
 * `camera_fb_t*` or any type owned by another component. The
 * `cam_stream_sink_t` shape is built from <esp_err.h>,
 * <stddef.h>, <stdbool.h>, <stdint.h> only — downstream consumers
 * do not need a transitive component on the include path (per
 * engram 4331).
 *
 * LIFECYCLE:
 *
 *     cam_stream_init()        one-shot at app_main, AFTER
 *                              cam_reader_init(); installs the
 *                              built-in disconnected sink
 *                              stubs and prepares state.
 *
 *     cam_stream_task_start() spawns the FreeRTOS task that
 *                              loops `cam_stream_loop_iteration`
 *                              forever.
 *
 *     cam_stream_sink_install(sink)
 *                              Called by the /ws/cams endpoint
 *                              on handshake accept to bind the
 *                              real server-side sink. Passing
 *                              NULL (or after a viewer
 *                              disconnects) reinstalls the
 *                              disconnected stubs.
 */

#ifndef IOT_CAMS_CAM_STREAM_H
#define IOT_CAMS_CAM_STREAM_H

#include "esp_err.h"

#include <stdbool.h>
#include <stddef.h>
#include <stdint.h>

#ifdef __cplusplus
extern "C" {
#endif

/* Sink function-pointer seam — installed by provisioning's WS
 * handler on handshake accept and reinstalled as NULL on
 * viewer close. Mirrors the reference project's ws_sink_t
 * shape (ws.h in esp32-cam-surveillance). */
typedef struct {
    esp_err_t (*send_bin)(const uint8_t *buf, size_t len);
    esp_err_t (*send_text)(const char *buf, size_t len);
    bool      (*is_connected)(void);
} cam_stream_sink_t;

/** One-shot at boot from app_main, after cam_reader_init().
 *
 *  Installs the built-in disconnected sink stubs (any send
 *  fails with ESP_ERR_INVALID_STATE; `is_connected` returns
 *  false). Does NOT spawn the stream task yet — call
 *  cam_stream_task_start() after this returns ESP_OK.
 *
 *  Idempotent: a second call returns ESP_ERR_INVALID_STATE.
 *
 *  @return ESP_OK on success; ESP_ERR_INVALID_STATE on a
 *          second init call.
 */
esp_err_t cam_stream_init(void);

/** Spawn the FreeRTOS stream task. Idempotent on host stubs.
 *
 *  @return ESP_OK on success (always in W1 stub; ESP_OK on
 *          FreeRTOS task spawn success in W2).
 */
esp_err_t cam_stream_task_start(void);

/** Install `sink` as the active sink. NULL (or any subsequent
 *  disconnect) reinstalls the built-in disconnected stubs.
 *  Pointer is copied by reference; the sink struct must
 *  outlive the stream task. */
void cam_stream_sink_install(const cam_stream_sink_t *sink);

/** True iff the installed sink reports a live viewer. */
bool cam_stream_sink_connected(void);

/** Cross-task counters. Lock-free u32 reads on Xtensa LX6.
 *  Deliberately separate from cam_reader's fb_drops so the
 *  status frame can report producer (cam_reader) and consumer
 *  (cam_stream) drops independently — same separation as the
 *  reference's stream.h:38-43.
 *
 *  @return monotonic counter values. Reset to zero only at
 *          boot. */
uint32_t cam_stream_frames_sent_get(void);
uint32_t cam_stream_frames_dropped_get(void);

/** Host-test seam: one iteration of the consume→send→release
 *  cycle:
 *
 *    fb = cam_reader_capture()
 *    rc = cam_stream_sink_send_bin(fb->buf, fb->len)
 *    cam_reader_release(fb)        // ALWAYS (REQ-ST-005)
 *    if (rc < 0) s_frames_dropped++
 *    else        s_frames_sent++
 *
 *  The FreeRTOS wrapper (W2: cam_stream_task_entry) calls
 *  this inside an infinite for-loop. Returns true if a frame
 *  was consumed and accepted by the sink, false on timeout
 *  or sink failure.
 */
bool cam_stream_loop_iteration(void);

#ifdef __cplusplus
}
#endif

#endif /* IOT_CAMS_CAM_STREAM_H */
