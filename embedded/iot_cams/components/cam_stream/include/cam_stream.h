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
 *                              forever at CAM_STREAM_PERIOD_MS.
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
 *  @return ESP_OK on success; ESP_ERR_INVALID_STATE on a
 *          second call.
 */
esp_err_t cam_stream_init(void);

/** Spawn the FreeRTOS stream task at the configured
 *  CAM_STREAM_PERIOD_MS period.
 *
 *  @return ESP_OK on FreeRTOS task spawn success. */
esp_err_t cam_stream_task_start(void);

/** Install `sink` as the active sink. NULL reinstalls the
 *  built-in disconnected stubs. */
void cam_stream_sink_install(const cam_stream_sink_t *sink);

/** True iff the installed sink reports a live viewer. */
bool cam_stream_sink_connected(void);

/** Internal seam accessor used by `cam_stream_sender.c`.
 *  Not part of the public surface; documented here only so
 *  the header is the single source of truth for the seam
 *  type. Other TUs do not need to call this. */
const cam_stream_sink_t *cam_stream_sink_get(void);

/** Push one binary frame through the installed sink.
 *  Currently a thin wrapper around `cam_stream_sink_get()`;
 *  W5 wraps a TX mutex around it so multiple producers
 *  (the stream task + future hello/status timers) cannot
 *  interleave wire bytes. */
esp_err_t cam_stream_sink_send_bin(const uint8_t *buf, size_t len);

/** Push one text frame through the installed sink. W5
 *  wraps a TX mutex around it. */
esp_err_t cam_stream_sink_send_text(const char *buf, size_t len);

/** Cross-task counters. Lock-free u32 reads on Xtensa LX6.
 *  Deliberately separate from cam_reader's fb_drops so the
 *  status frame can report producer (cam_reader) and consumer
 *  (cam_stream) drops independently — same separation as the
 *  reference's stream.h:38-43. */
uint32_t cam_stream_frames_sent_get(void);
uint32_t cam_stream_frames_dropped_get(void);

/** Stream-task default period, derived from a build-time
 *  constant until W8 wires CONFIG_FIRMWARE_STREAM_FPS_DEFAULT
 *  through Kconfig. Surfaced as a macro so the loop task and
 *  any external pacing logic (W3 hello+status timers) agree
 *  on the same period without a runtime indirection. */
#ifndef CAM_STREAM_PERIOD_MS
#define CAM_STREAM_PERIOD_MS 100  /* 10 FPS default */
#endif

/** Host-test seam: one iteration of the consume→send→release
 *  cycle. Honors `cam_reader.h`'s release contract precisely
 *  (ESP_ERR_TIMEOUT → no release; ESP_FAIL → release(NULL);
 *  ESP_OK → send/release/count). The FreeRTOS wrapper
 *  (`cam_stream_task_entry`) calls this inside an infinite
 *  for-loop paced at CAM_STREAM_PERIOD_MS via vTaskDelayUntil. */
bool cam_stream_loop_iteration(void);

#ifdef __cplusplus
}
#endif

#endif /* IOT_CAMS_CAM_STREAM_H */
