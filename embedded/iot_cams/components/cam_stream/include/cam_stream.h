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
 *  Wraps a TX mutex around the actual send so multiple
 *  producers (stream task + handshake hello + future
 *  status timer) never interleave wire bytes on the
 *  same fd. */
esp_err_t cam_stream_sink_send_bin(const uint8_t *buf, size_t len);

/** Push one text frame through the installed sink. Same
 *  TX-mutex wrap as send_bin. */
esp_err_t cam_stream_sink_send_text(const char *buf, size_t len);

/** Cross-task counters. Lock-free u32 reads on Xtensa LX6.
 *  Deliberately separate from cam_reader's fb_drops so the
 *  status frame can report producer (cam_reader) and consumer
 *  (cam_stream) drops independently — same separation as the
 *  reference's stream.h:38-43. */
uint32_t cam_stream_frames_sent_get(void);
uint32_t cam_stream_frames_dropped_get(void);

/** Stream-task default period, derived from the
 *  CONFIG_FIRMWARE_STREAM_FPS_DEFAULT Kconfig knob in
 *  provisioning/Kconfig.projbuild (mirrored in
 *  sdkconfig.defaults). Surfaced as a macro so the loop task
 *  and any external pacing logic agree on the same period
 *  without a runtime indirection. */
#ifdef CONFIG_FIRMWARE_STREAM_FPS_DEFAULT
#define CAM_STREAM_PERIOD_MS (1000 / CONFIG_FIRMWARE_STREAM_FPS_DEFAULT)
#else
#define CAM_STREAM_PERIOD_MS 100  /* 10 FPS fallback */
#endif

/* ---------- W3 — JSON text-frame payload schemas ----------
 *
 * The hello / status payloads are emitted as UTF-8 text WS
 * frames. Field order in the rendered JSON is INVARIANT —
 * downstream parsers (messaging-core, future web UI) MUST be
 * able to rely on the shape (mirrors REQ-WS-002 / REQ-WS-006
 * in the esp32-cam-surveillance reference).
 *
 * Capture-at-rest: every field in the structs is copied by
 * the caller once into the snapshot that feeds the wire
 * builders, so the builders themselves stay PURE — no IDF
 * runtime calls inside cam_stream_wire.c. The status-frame
 * timer (W5+) is responsible for repopulating the struct on
 * every fire.
 */

/** Identity fields populated by the WS endpoint at handshake
 *  accept time. Strings are NUL-terminated; empty name is
 *  allowed (unprovisioned devices before the operator
 *  configures a friendly name). */
typedef struct {
    char mac[13];    /* 12-hex eFuse MAC, lowercase, +NUL */
    char name[33];   /* Kconfig default or NVS override +NUL */
    char fw[16];     /* e.g. "0.1.0" +NUL */
} cam_stream_identity_t;

/** Status-telemetry snapshot. Populated by the WS handler
 *  from `esp_timer_get_time` / `esp_wifi_sta_get_rssi` /
 *  `esp_get_free_heap_size` / cam_reader's counters /
 *  cam_stream's counters. PURE wire builders, so the
 *  caller is the boundary to IDF. */
typedef struct {
    int64_t  uptime_us;
    int32_t  rssi_dbm;
    uint32_t free_heap;
    uint32_t fb_drops;        /* cam_reader, producer-side */
    uint32_t frames_sent;     /* cam_stream, consumer-side */
    uint32_t frames_dropped;  /* cam_stream, consumer-side */
    uint32_t fps_applied;     /* current effective FPS */
} cam_stream_status_metrics_t;

/** Render the hello frame into `out` (UTF-8, NUL-terminated
 *  on success — the NUL is NOT counted in the returned size).
 *
 *  Schema (REQ-WS-002 shape):
 *    {"type":"hello","mac":"<12-hex>","name":"<name>",
 *     "fw":"<fw>","caps":["jpeg","stream","identify"]}
 *
 *  @return bytes written excluding NUL, or 0 on overflow /
 *          invalid args (caller skips the send on the 0
 *          sentinel). */
size_t cam_stream_wire_build_hello(const cam_stream_identity_t *id,
                                     char *out, size_t out_len);

/** Render the status frame into `out`.
 *
 *  Schema (REQ-WS-006 shape):
 *    {"type":"status","mac":"<12-hex>","name":"<name>",
 *     "uptime_s":<int>,"rssi_dbm":<int>,"free_heap":<int>,
 *     "fb_drops":<int>,"frames_sent":<int>,
 *     "frames_dropped":<int>,"fps_applied":<int>}
 *
 *  @return bytes written excluding NUL, or 0 on overflow /
 *          invalid args. */
size_t cam_stream_wire_build_status(const cam_stream_status_metrics_t *m,
                                     const cam_stream_identity_t *id,
                                     char *out, size_t out_len);

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
