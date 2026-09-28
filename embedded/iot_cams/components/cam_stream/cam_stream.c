/* cam_stream.c — W1 SKELETON. No FreeRTOS task is spawned yet,
 * no loop body runs, no counters tick. The skeleton lands the
 * public surface, the disconnected-stub default sink, and the
 * counter accessors so the linker resolves every symbol the
 * public header declares. W2 replaces the bodies with the real
 * capture→send→release cycle and spawns the FreeRTOS task;
 * W5 binds the sink seam to the WS endpoint under the TX mutex.
 *
 * Scope of THIS file (W1):
 *   - cam_stream_init()        installs the disconnected sink
 *                              stubs, sets the init-done flag.
 *   - cam_stream_task_start()  logs the no-op skeleton line;
 *                              returns ESP_OK without spawning.
 *   - cam_stream_sink_install  / _connected  swap pointers.
 *   - cam_stream_frames_*_get  return 0 (counters land in W2).
 *   - cam_stream_loop_iteration()  returns false (real body
 *                              in W2).
 *
 * What is NOT in this file (lands in later work-units):
 *   - xSemaphoreCreateMutex + s_tx_mtx  (W5)
 *   - xTaskCreate + cam_stream_task_entry FreeRTOS wrapper  (W2)
 *   - JSON hello/status builders                           (W3)
 *   - Actual sink vtable with httpd_ws_send_frame_async     (W5)
 *   - IP_EVENT_STA_GOT_IP re-attach / STA_DISCONNECTED clear (W7)
 *     (those live in provisioning/src/ws_cams.c, not here)
 */

#include <stddef.h>

#include "esp_log.h"

#include "cam_stream.h"

static const char *TAG = "cam_stream";

/* ---------- module-static state (W1 only) ---------- */

static bool s_init_done = false;

/* ---------- disconnected sink stubs (default install) ----------
 *
 * Mirrors esp32-cam-surveillance's ws.c:60-86 pattern. Any send
 * against the disconnected stubs returns ESP_ERR_INVALID_STATE
 * and `is_connected` reports false. W2's loop body maps these
 * outcomes onto the dropped-frame counter path; W5's real
 * server-side sink overrides these with httpd_ws_send_frame_async.
 */

static esp_err_t sink_disconnected_send_bin(const uint8_t *buf,
                                             size_t len)
{
    (void)buf;
    (void)len;
    return ESP_ERR_INVALID_STATE;
}

static esp_err_t sink_disconnected_send_text(const char *buf,
                                              size_t len)
{
    (void)buf;
    (void)len;
    return ESP_ERR_INVALID_STATE;
}

static bool sink_disconnected_is_connected(void)
{
    return false;
}

static const cam_stream_sink_t s_sink_disconnected = {
    .send_bin     = sink_disconnected_send_bin,
    .send_text    = sink_disconnected_send_text,
    .is_connected = sink_disconnected_is_connected,
};

/* Active sink pointer. Never NULL after cam_stream_init —
 * always points to a fully-formed `cam_stream_sink_t` (one of
 * the disconnected stubs by default, the real server-side sink
 * once the /ws/cams handshake accept runs in W5). */
static const cam_stream_sink_t *s_sink = &s_sink_disconnected;

/* ---------- public API (skeleton bodies) ---------- */

void cam_stream_sink_install(const cam_stream_sink_t *sink)
{
    s_sink = (sink != NULL) ? sink : &s_sink_disconnected;
}

bool cam_stream_sink_connected(void)
{
    return s_sink->is_connected();
}

uint32_t cam_stream_frames_sent_get(void)
{
    /* W1 stub. W2 promotes this to a module-static u32 with
     * atomic-safe reads on Xtensa LX6. */
    return 0;
}

uint32_t cam_stream_frames_dropped_get(void)
{
    /* W1 stub. W2 mirrors the sent counter. */
    return 0;
}

bool cam_stream_loop_iteration(void)
{
    /* W1 stub. W2 implements the full capture→send→release
     * cycle as documented in cam_stream.h. */
    return false;
}

esp_err_t cam_stream_init(void)
{
    if (s_init_done) {
        ESP_LOGW(TAG, "init: already initialized — ignoring");
        return ESP_ERR_INVALID_STATE;
    }

    /* Default sink = disconnected stubs, so the loop body in
     * W2 can route every send through `s_sink` without a NULL
     * check. The real server-side sink lands in W5. */
    cam_stream_sink_install(NULL);

    s_init_done = true;
    ESP_LOGI(TAG,
             "init: skeleton ready "
             "(no task yet, no TX mutex yet, sink = disconnected)");
    return ESP_OK;
}

esp_err_t cam_stream_task_start(void)
{
    /* W1 stub. W2 lands the actual xTaskCreate call wrapping
     * cam_stream_loop_iteration in an infinite for-loop. */
    ESP_LOGI(TAG,
             "task_start: skeleton — no FreeRTOS task spawned yet "
             "(W2 will spawn it)");
    return ESP_OK;
}
