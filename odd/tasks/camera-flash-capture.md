# Feature: flash-enabled `GET /capture` for `embedded/iot_cams/`

## Goal

Add a `?flash=1` query parameter to the existing `/capture` endpoint so a
LAN client can request that the OV2640 capture be taken with the
AI-Thinker ESP32-CAM's onboard flash LED (GPIO 4) illuminated. The
firmware accepts the flag unconditionally — the clock-window logic
(17:45–05:45) lives in the surveillance **worker**, not here.

The flash implementation must:

1. Light the LED **before** the sensor integrates the frame (pre-charge),
   so the OV2640's auto white balance has stabilized by the time
   `esp_camera_fb_get` returns.
2. **Not damage the LED** over time — the AI-Thinker board has no
   current-limiting resistor on GPIO 4 (Mi-Bee teardown), so we drive
   at reduced PWM duty instead of full on/off.
3. **Not conflict** with the camera's own use of LEDC Timer 0 / Channel 0
   (XCLK generation). The flash must use Timer 1 / Channel 1.
4. Be a thin addition to `cam_reader` and `sta_server` — no sensor
   reconfiguration, no Kconfig changes, no partition-table changes.

## Design decisions (locked)

| Decision | Choice | Rationale |
| --- | --- | --- |
| Flash trigger | Plain query parameter `?flash=1` on `GET /capture` | Matches existing `/capture` shape; absent or any other value = flash off (current behavior) |
| Pre-charge delay | 200 ms between LED on and `esp_camera_fb_get` | Community-tested value from Mi-Bee's deployment (blog post + open-source firmware). Lets OV2640 AWB stabilize. 50 ms was too short per the user's own blurry-image observation. |
| LED control mode | LEDC PWM at ~80 % duty (205/255 @ 2 kHz, 8-bit) | Mi-Bee teardown: AI-Thinker ESP32-CAM has no current-limiting resistor on GPIO 4. 80 % duty at 2 kHz is bright enough for a usable image and safe for continuous board wear |
| LEDC timer / channel | `LEDC_TIMER_1` + `LEDC_CHANNEL_1` | Camera driver owns `LEDC_TIMER_0` / `LEDC_CHANNEL_0` for XCLK generation (`cam_reader.c:65-66`). Using Timer/Channel 0 for the flash crashes or stalls the camera |
| Flash GPIO | `GPIO_NUM_4` (AI-Thinker onboard LED) | Hardwired on the board; same pin the user already uses for the same purpose on the reference ESP32-CAM project |
| AEC + flash interaction | Disable `sensor->set_aec2(sensor, 0)` at init | Tasmota community finding (arendst/Tasmota#23222): AECDSP overrides `AELevel` and produces washed-out colors. Disabling AECDSP lets the OV2640's auto-exposure adapt to the flash-lit scene properly |
| Always-off guarantee | `gpio_set_level`/LEDC duty reset on every exit path, including the 503 / 500 paths | Prevents a wedged flash if the capture errors after LED-on |
| Query parser | `httpd_query_key_value()` from `esp_http_server` | Stock ESP-IDF API; handles URL-decoding; no hand-rolled string parsing |

## Public API additions

```c
/* cam_reader.h — additions */

esp_err_t cam_reader_flash_init(void);                       /* one-shot at boot; LEDC + AEC setup */
esp_err_t cam_reader_capture_with_flash(camera_fb_t **fb, bool flash);
```

`cam_reader_capture_with_flash` flow:

```
flash == false:
    return cam_reader_capture(fb)       // existing path, byte-for-byte unchanged

flash == true:
    ledc_set_duty(LOW_SPEED, CH1, 205)
    ledc_update_duty(LOW_SPEED, CH1)
    vTaskDelay(pdMS_TO_TICKS(200))      // AWB pre-charge
    err = cam_reader_capture(fb)        // existing sema-take + fb_get
    ledc_set_duty(LOW_SPEED, CH1, 0)    // ALWAYS off after, even on error
    ledc_update_duty(LOW_SPEED, CH1)
    return err
```

`cam_reader_capture` is **not modified**. Every existing caller
(streaming, future WebSocket) keeps its no-flash behavior.

## `/capture?flash=1` request shape

```
GET /capture HTTP/1.1              ← no flash, current behavior
GET /capture?flash=1 HTTP/1.1      ← LED on for 200 ms before frame, then off
GET /capture?flash=0 HTTP/1.1      ← no flash (any value ≠ "1" means no flash)
```

Response contract is unchanged: `Content-Type: image/jpeg`,
`Content-Disposition: inline; filename=capture.jpg`,
`Cache-Control: no-store`. Status codes unchanged (200 / 503 / 500).

## Files touched

| File | Change |
| --- | --- |
| `components/cam_reader/include/cam_reader.h` | Add `cam_reader_flash_init`, `cam_reader_capture_with_flash` declarations |
| `components/cam_reader/cam_reader.c` | Add LEDC init, `cam_reader_capture_with_flash`, AEC disable at init |
| `components/provisioning/src/sta_server.c` | Parse `?flash=1` in `capture_get_handler`; dispatch to capture-with-flash |
| `main/iot_cams.c` | Call `cam_reader_flash_init()` once after `cam_reader_init()` |

No changes to:
- `sdkconfig.defaults`, `sdkconfig`, `partitions.csv`
- `Kconfig.projbuild`
- `/whoami` handler, `/ws/cams` handler, WS streaming
- The 5 s mutex budget or `cam_reader_capture` internals

## Tasks

- [ ] T1 — `cam_reader_flash_init()`: LEDC Timer 1 / Channel 1 setup, GPIO 4
  configured as output, duty set to 0, `sensor->set_aec2(sensor, 0)`.
- [ ] T2 — `cam_reader_capture_with_flash()`: 200 ms pre-charge + existing
  `cam_reader_capture()` body, LED off on every exit path.
- [ ] T3 — `sta_server.c`: parse `?flash=1` via `httpd_query_key_value`,
  dispatch to `cam_reader_capture_with_flash(&fb, flash)` instead of
  `cam_reader_capture(&fb)`.
- [ ] T4 — `main/iot_cams.c`: one-line call to `cam_reader_flash_init()`
  right after `cam_reader_init()`.
- [ ] T5 — `idf.py build` clean, zero warnings on the existing toolchain.
- [ ] T6 — Operator-flash on one device, verify `?flash=1` and no-`?flash=1`
  both return valid JPEG. Optional: manual visual check that flash frame
  is properly illuminated vs the no-flash frame.

## Acceptance criteria

- `idf.py build` exits 0 with zero new warnings.
- `GET /capture` (no query) returns the same image contract as today.
- `GET /capture?flash=1` returns the same image contract, with the flash
  LED visibly on for ~200 ms before the JPEG is sent.
- `GET /capture?flash=0`, `GET /capture?flash=anything`, and `GET /capture`
  all behave identically (no flash).
- The `/whoami` and `/ws/cams` endpoints are not affected.
- The mutex budget (5 s) and the semaphore ownership rules in
  `cam_reader_capture` / `cam_reader_release` are unchanged.

## Non-goals (explicit)

- No automatic brightness detection (JPEG-size proxy, grayscale sampling).
  The clock-window decision lives in the surveillance worker.
- No change to image resolution, JPEG quality, vflip, or PSRAM buffer
  configuration.
- No new `/capture` parameter for brightness or PWM duty (configurable
  via build-time constants only).
- No changes to the OTA path, provisioning flow, or the softAP captive
  portal.
