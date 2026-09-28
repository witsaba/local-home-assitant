/* cam_reader.h — public API for the iot_cams camera reader.
 *
 * Owns the AI-Thinker ESP32-CAM OV2640 bring-up (lifted from
 * `../rural_home_assistant/backend/iot-camera/components/cam_reader/`)
 * and a binary semaphore that serializes access to the camera
 * hardware across every consumer — httpd handlers in this firmware,
 * and any future FreeRTOS task / WebSocket / MQTT path.
 *
 * The mutex is non-recursive. At most one `esp_camera_fb_get` runs
 * system-wide at any time; concurrent callers see `ESP_ERR_TIMEOUT`
 * from `cam_reader_capture()` and should map that to HTTP 503.
 *
 * LIFECYCLE:
 *
 *     cam_reader_init()        one-shot at app_main; boots the sensor
 *                              and creates the binary semaphore in
 *                              the unlocked state.
 *
 *     cam_reader_capture(&fb)  take the next JPEG frame (5 s wait
 *                              budget on the mutex; ESP_ERR_TIMEOUT
 *                              beyond that).
 *
 *     cam_reader_release(fb)   return the frame buffer to the driver
 *                              AND give the mutex. NULL fb is
 *                              permitted — the buffer return is a
 *                              no-op and only the mutex is given.
 *                              The caller MUST call this on every
 *                              `cam_reader_capture` outcome except
 *                              the `ESP_ERR_TIMEOUT` path (where the
 *                              mutex was never taken).
 */

#ifndef IOT_CAMS_CAM_READER_H
#define IOT_CAMS_CAM_READER_H

#include "esp_err.h"
#include "esp_camera.h"

#ifdef __cplusplus
extern "C" {
#endif

/** Boot the OV2640 sensor and create the camera mutex.
 *
 *  Idempotent: returns `ESP_ERR_INVALID_STATE` on a second call.
 *  On success the sensor is ready and the mutex is unlocked.
 *
 *  @return ESP_OK on success, otherwise an esp_err_t propagated
 *          from `esp_camera_init` or `xSemaphoreCreateBinary`.
 */
esp_err_t cam_reader_init(void);

/** Take the camera mutex and return the next JPEG frame.
 *
 *  Blocks up to 5 s on the mutex.
 *
 *  @param[out] fb On success, *fb is non-NULL. On either failure
 *                 outcome (timeout or sensor failure), *fb is NULL.
 *
 *  @return
 *    - `ESP_OK`            mutex acquired and `esp_camera_fb_get`
 *                          returned a valid frame; *fb is non-NULL.
 *    - `ESP_ERR_TIMEOUT`   mutex contention; the 5 s budget elapsed.
 *                          *fb is NULL. Caller should NOT call
 *                          `cam_reader_release` (the mutex was
 *                          never taken).
 *    - `ESP_FAIL`          mutex acquired but `esp_camera_fb_get`
 *                          returned NULL. *fb is NULL. Caller MUST
 *                          call `cam_reader_release(NULL)` to drop
 *                          the mutex.
 *    - `ESP_ERR_INVALID_ARG` *fb was NULL on the way in.
 *    - `ESP_ERR_INVALID_STATE` `cam_reader_init` not called.
 */
esp_err_t cam_reader_capture(camera_fb_t **fb);

/** Return the frame buffer to the driver pool and give the mutex.
 *
 *  Safe to call with `fb == NULL`; the buffer return is a no-op and
 *  only the mutex is given. This is the right call to pair with the
 *  `ESP_FAIL` outcome of `cam_reader_capture()`.
 */
void cam_reader_release(camera_fb_t *fb);

/** Producer-side counters. Lock-free u32 reads on Xtensa LX6.
 *  Bumped once per `cam_reader_capture` outcome:
 *    - `frames_captured` — successful `esp_camera_fb_get`.
 *    - `fb_drops`        — `esp_camera_fb_get` returned NULL
 *                          (after the mutex was taken); the
 *                          mutex has been released automatically.
 *  Called by the cam_stream status-frame builder in W5+.
 *
 *  @return monotonic counter values. Reset to zero only at boot. */
uint32_t cam_reader_frames_captured_get(void);
uint32_t cam_reader_fb_drops_get(void);

#ifdef __cplusplus
}
#endif

#endif /* IOT_CAMS_CAM_READER_H */
