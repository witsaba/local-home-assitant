# Feature: take-picture endpoint for `embedded/iot_cams/`

## Goal

Add `GET /capture` to the iot_cams firmware so a LAN client can poll
the OV2640 for a single JPEG frame — same contract as
`esp32-cam-surveillance`'s reference endpoint, but hosted inside
iot_cams's existing STA httpd handle (alongside `/whoami`).

## Design decisions (locked)

| Decision | Choice | Rationale |
| --- | --- | --- |
| Sensor lifecycle | `init_camera()` runs once from `app_main` after `provisioning_join_ap()` | Camera stays up for the device's lifetime; matches the reference's never-deinit behavior |
| Concurrency guard | Binary semaphore owned by a new `cam_reader` component | Camera hardware is the resource being protected — guard lives with the resource, not with the httpd |
| Semaphore API | `cam_reader_capture(&fb)` / `cam_reader_release(fb)` | Single acquisition/release point; `release(NULL)` permitted (sema-give only) |
| Semaphore wait | 5 s timeout | Mirrors the reference; concurrent caller past 5 s gets 503 |
| Endpoint placement | `components/provisioning/src/sta_server.c` | Same httpd handle as `/whoami`, same audience (post-provisioning LAN clients), not on the softAP |
| Component layout | New sibling `components/cam_reader/` | Mirrors the reference's `cam_reader` component; path-dep from `main/idf_component.yml` |
| Camera config | `PIXFORMAT_JPEG`, `FRAMESIZE_SVGA` (800×600), `jpeg_quality=12`, `grab_mode=CAMERA_GRAB_LATEST` | Byte-for-byte the reference config; AI-Thinker pin map is identical |
| PSRAM | Drive `fb_count` / `fb_location` from `esp_psram_get_size()` at boot | Reference pattern; PSRAM detection is per-boot |
| Managed dep | `espressif/esp32-camera` declared in `components/cam_reader/idf_component.yml` | Component owns its own external dep — clean lift to a separate repo later (URL swap only) |

## Public API

```c
/* cam_reader.h */
#include "esp_err.h"
#include "esp_camera.h"

esp_err_t cam_reader_init(void);                              /* one-shot at boot */
esp_err_t cam_reader_capture(camera_fb_t **fb);               /* sema-take + fb_get, 5 s budget */
void      cam_reader_release(camera_fb_t *fb);                /* fb_return + sema-give; NULL-safe */
```

`cam_reader_capture` outcomes:

| Outcome | Return | `*fb` | Caller's next action |
| --- | --- | --- | --- |
| Mutex acquired, frame taken | `ESP_OK` | non-NULL | `httpd_resp_send(req, fb->buf, fb->len); cam_reader_release(fb);` |
| Mutex timeout (> 5 s) | `ESP_ERR_TIMEOUT` | NULL | HTTP 503 `text/plain`; **do not** call `cam_reader_release` |
| `esp_camera_fb_get` failed | `ESP_FAIL` | NULL | HTTP 500; **must** call `cam_reader_release(NULL)` to drop the mutex |

## `/capture` response contract

```
HTTP/1.1 200 OK
Content-Type: image/jpeg
Content-Disposition: inline; filename=capture.jpg
Cache-Control: no-store, no-cache, must-revalidate, max-age=0
<fb->len bytes of JPEG, single httpd_resp_send>
```

HTTP 503 on mutex timeout; HTTP 500 on sensor failure.

## Tasks

### W1 — port `cam_reader` component (with take/release API + binary semaphore)
- `components/cam_reader/CMakeLists.txt`
- `components/cam_reader/idf_component.yml` (managed dep on `esp32-camera`)
- `components/cam_reader/include/cam_reader.h`
- `components/cam_reader/cam_reader.c` — pin map from reference + semaphore init in `cam_reader_init()`, semaphore take in `cam_reader_capture()`, semaphore give in `cam_reader_release()`
- Source-of-truth lifted from `../rural_home_assistant/backend/iot-camera/components/cam_reader/` and extended with the take/release API; the reference only exposes `init_camera()` because its only consumer is the http server.
- Work-unit commit: `feat(cam-reader): port cam_reader from esp32-cam-surveillance with take/release API`

### W2 — wire `cam_reader` into iot_cams
- `main/idf_component.yml`: add `cam_reader` as a path-dep.
- `main/CMakeLists.txt`: add `cam_reader` to `REQUIRES`.
- `main/iot_cams.c`: include `cam_reader.h`, call `cam_reader_init()` from `app_main` after `provisioning_join_ap()` (already-provisioned path) and after `provisioning_run()` (post-provisioning path).
- Update the "It includes ONLY `provisioning.h`" comment block at the top of `iot_cams.c` to reflect the new include surface.
- Work-unit commit: `feat(iot-cams): wire cam_reader into app + esp32-camera managed dep`

### W3 — add `/capture` handler to STA server
- `components/provisioning/src/sta_server.c`: register `GET /capture` → new `capture_get_handler` that calls `cam_reader_capture` / `cam_reader_release`.
- Add `cam_reader` to the provisioning component's `REQUIRES` so the linker sees its symbols.
- Work-unit commit: `feat(iot-cams): add GET /capture endpoint on STA server`

### W4 — build verification
- `rm -rf build dependencies.lock && idf.py reconfigure && idf.py build` — confirm exit 0, zero warnings, give back the binary size of `iot_cams.bin` as evidence.
- The board/conventional-commit style engram demands `idf.py build` end-to-end with no warnings, per `embedded/iot_cams/build-policy`.
- Work-unit commit: `chore(iot-cams): record idf.py build verification`

### W0 — ODD task file (this document)
- Work-unit commit: `docs(odd): capture-endpoint task plan`

## Out of scope (follow-ups, not part of this branch)

- WebSocket control plane on top of `/capture` for streaming.
- Camera settings UI mirroring the reference `/config` + `/api/settings` (28+ parameters).
- iot_cams's workers / discovery service consuming `/capture` (the workers service already discovers the camera via `/whoami`; plugging the captured image into the storage path is a separate concern).
- SoftAP-time capture (operator UX during provisioning).
- Runtime-tunable frame size / quality (today's 800×600 + quality 12 is build-time).

## Acceptance criteria

1. `idf.py build` exits 0 on a freshly cleaned tree, with the same warning count as `main` (zero new warnings).
2. `GET http://<device-ip>/capture` returns a valid JPEG that opens in a standard image viewer.
3. `GET /capture` issued twice in parallel results in one 200 + one 503 (or both 200, serialized), never a crash, never both 500.
4. The device stays in softAP provisioning until credentials are committed (camera init does not block provisioning).
5. The softAP httpd (port 80 on `192.168.4.1`) keeps serving `/` for the duration of provisioning — `/capture` is intentionally not registered there.

## Tracking

Mirrored to Engram under project `local-home-assitant`, topic key
`iot_cams/capture-endpoint`. `todo` list reflects this plan.
