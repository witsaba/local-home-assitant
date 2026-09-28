# Feature: WebSocket `/ws/cams` endpoint for `embedded/iot_cams/`

## Goal

Add a server-mode WebSocket endpoint at `GET /ws/cams` on the iot_cams
STA httpd (the same handle that already serves `/whoami`, `/scan`,
`/capture`) so a single LAN client can subscribe to a live JPEG
stream from the camera. Wire format is one complete JPEG per WS
binary message; intermittent JSON text frames carry the handshake
hello and the periodic status telemetry. Single-viewer policy: the
second concurrent handshake gets a rejection frame and the socket is
closed.

This branch ships **chip-side only**. The messaging-core bridge
(WebSocket client + NATS fan-out to web users) is a separate workstream
in `local-home-assistant`. The chip does not know about NATS, the
broker, or the web app; it serves `/ws/cams` and that is the entire
contract.

## Design decisions (locked)

| Decision | Choice | Rationale |
| --- | --- | --- |
| URI path | `/ws/cams` | `/ws/` prefix matches HTTP convention for WebSocket resources; disambiguates from any future RTSP or MJPEG endpoint |
| Endpoint placement | `components/provisioning/src/ws_cams.c` registered on the existing STA httpd | Same handle as `/whoami` and `/capture`; one port, one audience (post-provisioning LAN clients); never on the softAP httpd |
| Concurrency guard | **Reuse `cam_reader_capture` / `cam_reader_release`** as the camera mutex | Avoid introducing a producer/consumer queue in first cut; `/capture` and `/ws/cams` already serialize cleanly via the 5 s binary semaphore. `/capture` is starved (5 s timeout → 503) while WS is streaming — acceptable per design. |
| Stream producer | Dedicated FreeRTOS task `cam_stream_task` | Long-lived WS shouldn't pin the httpd worker pool; dedicated task with its own scheduling |
| Task stack + priority | Mirror `cam_reader_init`'s prio / stack conventions (PrioUseCapture task) | Capture and stream are siblings, peers in resource access |
| Capture gate | **Soft**: stream loop wakes every frame period; on viewer disconnect, sink goes NULL and the loop blocks on `cam_reader_capture` until a new viewer handshakes | Mains-powered always-on device; NO-SOI warm-up cost (engram 4334) is harmless after first boot. The reference's FW-19 hard gate is heavier and not needed here. Add only if NO-SOI noise becomes an operator concern. |
| Wire format (binary) | One complete JPEG per WS message, `pkt.final=true`, op=0x2; payload = `camera_fb_t.buf`, len = `camera_fb_t.len` | Lowest overhead. SVGA q12 ≈ 28 KB per frame (measured engram 4336); far below any framing limit. No fragmentation layer. |
| Wire format (text) | `{"type":"hello",...}` first text frame of session; `{"type":"status",...}` every 30 s while connected | Same shape as the esp32-cam-surveillance reference (`ws_text_frame.c`). Field order is invariant per the reference's REQ-WS-002 / REQ-WS-006 schemas. |
| Single-viewer policy | Second `GET /ws/cams` while one is open → short text error `{"type":"error","reason":"viewer_limit"}` then `ESP_FAIL` (closes socket). Server-wide `max_open_sockets` stays untouched — `/whoami` and `/capture` keep working alongside a viewer. | Avoids IDFGH-7162 / 8004 (engram from prior research) socket-exhaustion footguns. messaging-core owns the only inbound WS client; one viewer is correct and conservative. |
| Sink seam | `cam_stream_sink_install(sink)` at handshake accept; `cam_stream_sink_install(NULL)` (built-in disconnected stubs) at viewer close and at boot default | Single source of truth for "is a viewer live right now" — mirrors the reference's `ws_sink_t` pattern (`ws.c:104-122`) |
| TX serialization | `xSemaphoreCreateMutex()` (default `s_tx_mtx`) around every `cam_stream_sink_send_*` call, created in `cam_stream_init()` before any sink install | Multiple producers (`cam_stream_task` + status timer + hello from handshake) write to the same fd; without serialization wire frames interleave |
| Frame-buffer ownership | Stream task owns the `camera_fb_t` from `cam_reader_capture` return until `cam_reader_release`. The release call is **always** issued on every exit path — success or failure — same invariant as the `/capture` handler. | Engram 4330 REQ-ST-005 — same leak-free contract |
| WiFi lifecycle | `ws_cams.c` subscribes directly to raw IDF events `IP_EVENT_STA_GOT_IP` and `WIFI_EVENT_STA_DISCONNECTED` (mirrors provisioning.c:274-281). On IP-up: register the URI on the live STA httpd, idempotent (a `s_registered` flag, single-shot per httpd lifetime). On disconnect: clear `s_registered` + `s_viewer_fd` and reinstall the disconnected sink. | Same fix that resolved engram 4278's `/capture`-after-WiFi-blip bug; the reference solved this same class of bug in `ws_server.c:282-355`. |
| Component layout | New sibling `components/cam_stream/` (4 files mirroring `cam_reader`); new `ws_cams.c` inside `provisioning` (the WS endpoint belongs with the httpd that hosts it) | Mirrors the `/capture` architecture exactly; lifted from the surveillance reference with one deliberate divergence (no producer task yet) |
| Identity source | `esp_efuse_mac_get_default()` for the canonical 12-hex MAC, formatted lowercase; `identity.name` from NVS provisioning data, may be empty; `fw` = `FW_VERSION` from `main/iot_cams.c` | Mirrors the existing `/whoami` JSON shape so the operator sees the same MAC regardless of transport |
| PSRAM | Reuse existing `CONFIG_SPIRAM=y / MODE_QUAD / SPEED_40M / USE_MALLOC`; `cam_reader` already gates on `#if CONFIG_SPIRAM_SUPPORT` to select single vs double fb_count | No new project-config change needed. PSRAM requirement is identical to `/capture` (engram 4333). |
| Partition table | Reuse existing 1.5 MB factory partition from the `/capture` PR (engram 4336). The WS endpoint adds ~10–15 KB to the binary; we have ~510 KB of headroom. | Defer re-tuning until W8 build sanity (post-commit verify) |
| Managed deps | **No new deps in first cut** | cJSON deferred with the `{"cmd":"stream"}` control-plane follow-up |
| Default FPS | 10 | Fits the SVGA q12 sweet spot we measured during `/capture` validation; widens to 25 max via sdkconfig |
| Status cadence | 30 s | Matches the reference's `CONFIG_FIRMWARE_WS_STATUS_PERIOD_MS`; reflects operator tolerance for telemetry, not video |

## Public API

```c
/* components/cam_stream/include/cam_stream.h */
#include "esp_err.h"
#include "esp_camera.h"

#include <stdbool.h>
#include <stddef.h>
#include <stdint.h>

#ifdef __cplusplus
extern "C" {
#endif

/* Sink function-pointer seam, installed by the WS endpoint on
 * handshake accept and cleared by the stream task on disconnect.
 * Mirrors the reference's ws_sink_t (ws.h:197-204). */
typedef struct {
    esp_err_t (*send_bin)(const uint8_t *buf, size_t len);
    esp_err_t (*send_text)(const char *buf, size_t len);
    bool      (*is_connected)(void);
} cam_stream_sink_t;

/* One-shot at boot from app_main, after cam_reader_init(). */
esp_err_t cam_stream_init(void);

/* Spawn the FreeRTOS stream task. Idempotent on host stubs. */
esp_err_t cam_stream_task_start(void);

/* Install / clear the sink (NULL → disconnected stubs). */
void cam_stream_sink_install(const cam_stream_sink_t *sink);
bool cam_stream_sink_connected(void);

/* Cross-task counters, lock-free u32 reads on Xtensa LX6.
 * Deliberately separate from cam_reader drops so the JSON
 * status frame can report both producer and consumer
 * independently (same separation as reference stream.h:38-43). */
uint32_t cam_stream_frames_sent_get(void);
uint32_t cam_stream_frames_dropped_get(void);

/* Host-test seam: one iteration of consume→send→release.
 * The FreeRTOS wrapper calls this inside an infinite loop. */
bool cam_stream_loop_iteration(void);

#ifdef __cplusplus
}
#endif
```

## Wire contract

```
GET /ws/cams HTTP/1.1
Host: <device-ip>
Upgrade: websocket
Connection: Upgrade
Sec-WebSocket-Key: <base64-of-16-random-bytes>
Sec-WebSocket-Version: 13

HTTP/1.1 101 Switching Protocols
Upgrade: websocket
Connection: Upgrade
Sec-WebSocket-Accept: <base64-of-SHA1-of-key-+-guid>
```

After the 101 upgrade, the server emits one TEXT frame then alternates
BINARY + (every 30 s) TEXT.

```jsonc
// hello — first text frame of session (REQ-WS-002 shape)
{
  "type": "hello",
  "mac":  "e08cfe3091b0",
  "name": "iot-cam-1",          // may be empty
  "fw":   "0.1.0",
  "caps": ["jpeg", "stream", "identify", "control"]
}

// status — every CONFIG_FIRMWARE_WS_STATUS_PERIOD_MS while open
{
  "type":          "status",
  "mac":           "e08cfe3091b0",
  "name":          "iot-cam-1",
  "uptime_s":      1247,
  "rssi_dbm":     -58,
  "free_heap":     87432,
  "fb_drops":      3,            // producer-side, from cam_reader
  "frames_sent":   37210,        // consumer-side sent counter
  "frames_dropped":7,            // consumer-side drop counter
  "fps_applied":   10
}

// second-handshake rejection; server closes socket after sending
{"type":"error","reason":"viewer_limit"}
```

Field order in hello and status is **invariant**; downstream parsers
must be able to rely on the shape (mirrors REQ-WS-002 / REQ-WS-006 in
the reference).

## Tasks

### W1 — port `cam_stream` component skeleton
- New: `embedded/iot_cams/components/cam_stream/CMakeLists.txt`
- New: `embedded/iot_cams/components/cam_stream/idf_component.yml`
- New: `embedded/iot_cams/components/cam_stream/include/cam_stream.h`
- New: `embedded/iot_cams/components/cam_stream/cam_stream.c` (init + counter stubs + sink stub; no task spawned yet, no loop body)
- Modify: `embedded/iot_cams/main/idf_component.yml` — add `cam_stream: path: ../components/cam_stream`
- Modify: `embedded/iot_cams/main/CMakeLists.txt` — add `cam_stream` to `REQUIRES`
- Work-unit commit: `feat(cam-stream): port cam_stream component skeleton`
- Acceptance: `rm -rf build dependencies.lock && idf.py reconfigure && idf.py build` exits 0 with no new warnings.

### W2 — capture→send→release loop + counters
- Modify: `embedded/iot_cams/components/cam_stream/cam_stream.c` — implement `cam_stream_loop_iteration()` (calls `cam_reader_capture(&fb)`, then `cam_stream_sink_send_bin(...)`, then `cam_reader_release(fb)`, on sink failure increment `s_frames_dropped` and continue), and the FreeRTOS wrapper `cam_stream_task_entry` under `#ifndef UNITY_HOST_BUILD`. Add `cam_stream_task_start()`.
- New: `embedded/iot_cams/components/cam_stream/cam_stream_sender.c` — thin wrapper around `cam_stream_sink_send_bin` with the no-op / `ESP_ERR_INVALID_STATE` failure path.
- Modify: `embedded/iot_cams/main/iot_cams.c` — include `cam_stream.h`, call `cam_stream_init()` then `cam_stream_task_start()` after `cam_reader_init()`. Update the "It includes ONLY `provisioning.h` and `cam_reader.h`" comment block to add `cam_stream.h`.
- Work-unit commit: `feat(cam-stream): capture-send-release loop + stream task`
- Acceptance: `idf.py build` exits 0. With no viewer connected (`cam_stream_sink_install(NULL)` is the default) the loop logs `stream: no viewer, frame dropped` every iteration and `frames_dropped` increments.

### W3 — JSON text-frame builders (hello + status)
- New: `embedded/iot_cams/components/cam_stream/cam_stream_wire.c`
- Two pure builders:
  - `cam_stream_wire_build_hello(const device_identity_t *id, char *out, size_t out_len)` → returns bytes written or 0 on overflow
  - `cam_stream_wire_build_status(const cam_stream_status_metrics_t *m, const device_identity_t *id, char *out, size_t out_len)`
- Public header additions to `cam_stream.h`: the two function signatures and the `cam_stream_status_metrics_t` struct (fields mirror the JSON above).
- Work-unit commit: `feat(cam-stream): json text-frame builders (hello + status)`
- Acceptance: `idf.py build` exits 0. No public behavioral change yet.

### W4 — provision `/ws/cams` WS endpoint + handshake accept
- New: `embedded/iot_cams/components/provisioning/src/ws_cams.c` — the WS endpoint handlers; registers the `cam_stream_sink_t` vtable on handshake.
- Modify: `embedded/iot_cams/components/provisioning/src/sta_server.c` — register `GET /ws/cams` URI handler with `.uri="/ws/cams"`, `.method=HTTP_GET`, `.handler=ws_cams_handler`, `.is_websocket=true`. (The `sta_server.c` file already drives the existing `/whoami` and is the right place to add the new URI; no public API change for `provisioning_start_sta_server()`.)
- Modify: `embedded/iot_cams/components/provisioning/CMakeLists.txt` — add `cam_stream` to `REQUIRES`.
- Work-unit commit: `feat(provisioning): register /ws/cams websocket endpoint`
- Acceptance: `idf.py build` exits 0. `websocat ws://<device-ip>/ws/cams` performs the 101 upgrade and the server holds the connection open; no frames emitted yet (sink install + frame loop wired in W5).

### W5 — bind sink seam to stream task under TX mutex
- Modify: `embedded/iot_cams/components/cam_stream/cam_stream.c` — add `s_tx_mtx = xSemaphoreCreateMutex()` in `cam_stream_init()` (BEFORE any sink install; failed creation is fatal). Wrap `cam_stream_sink_send_bin` and `cam_stream_sink_send_text` in `xSemaphoreTake(s_tx_mtx) / xSemaphoreGive`.
- New: `embedded/iot_cams/components/provisioning/src/ws_cams.c:server_sink_*` — defines `server_sink_send_bin`, `server_sink_send_text`, `server_sink_is_connected` as static functions. The sink uses `httpd_ws_send_frame_async(httpd_handle_for_send, captured_fd, &pkt)`. Any send failure calls `viewer_clear()` (which calls `cam_stream_sink_install(NULL)` + `cam_stream_status_timer_stop()`).
- Modify: `embedded/iot_cams/components/cam_stream/cam_stream_sender.c` — wire `cam_stream_loop_iteration()` to actually call `cam_stream_sink_send_bin(fb->buf, fb->len)` instead of the W2 stub.
- Work-unit commit: `feat(cam-stream): bind ws sink seam to stream task under tx mutex`
- Acceptance: `websocat ws://<device-ip>/ws/cams` receives BINARY frames at ~10 Hz, each a decodable JPEG, consecutive MD5 hashes distinct.

### W6 — single-viewer policy + rejection frame
- Modify: `embedded/iot_cams/components/provisioning/src/ws_cams.c:ws_cams_handler` — when `httpd_req_to_sockfd(req)` returns a different fd than the captured `s_viewer_fd`, send `{"type":"error","reason":"viewer_limit"}` on the new socket's WS frame and return `ESP_FAIL` (closes that socket). The active viewer's session is untouched.
- Work-unit commit: `feat(cam-stream): single-viewer policy with viewer_limit rejection`
- Acceptance: two parallel `websocat` to `/ws/cams` → first holds the stream; second receives `viewer_limit` text frame and the socket is closed by the server. First is unaffected. `/whoami` and `/capture` keep working alongside the first viewer.

### W7 — IP-up re-attach + STA-disconnect clear
- Modify: `embedded/iot_cams/components/provisioning/src/ws_cams.c` — subscribe to IDF events `IP_EVENT_STA_GOT_IP` and `WIFI_EVENT_STA_DISCONNECTED` (the same handler pattern used in `provisioning.c:274-281`). On IP-up: register the `/ws/cams` URI handler on the live httpd; idempotent (a `s_registered` flag guards re-registration). On disconnect: clear `s_httpd`, `s_registered`, `s_viewer_fd` and call `cam_stream_sink_install(NULL)` — drops the dead-handle sink so the next IP-up re-attaches cleanly.
- Work-unit commit: `fix(wifi): re-attach /ws/cams on sta reconnect; clear on disconnect`
- Acceptance: `idf.py monitor` + forced wifi down/up while a viewer is connected → log shows `viewer disconnected (send failed)`, slot freed, next `/ws/cams` handshake on the fresh IP succeeds within 1 s of `wifi up`. Pre-fix: 404 on the new IP until power cycle (same bug class as engram 4278).

### W8 — sdkconfig tunables
- Modify: `embedded/iot_cams/sdkconfig.defaults` — add:
  - `CONFIG_HTTPD_WS_SUPPORT=y`
  - `CONFIG_FIRMWARE_STREAM_FPS_DEFAULT=10`
  - `CONFIG_FIRMWARE_STREAM_FPS_MIN=1`
  - `CONFIG_FIRMWARE_STREAM_FPS_MAX=25`
  - `CONFIG_FIRMWARE_WS_STATUS_PERIOD_MS=30000`
  - `CONFIG_FIRMWARE_WS_PATH="/ws/cams"`
  - mirror `components/provisioning/Kconfig.projbuild` defaults for the new symbols
- Work-unit commit: `feat(iot-cams): sdkconfig knobs for ws-cams endpoint`
- Acceptance: `idf.py build` exits 0; `idf.py menuconfig` shows the new tunables under the same menu as the WiFi provisioning tunables.

### W9 — post-W8 build sanity (no commit)
- Manual gate. `rm -rf build dependencies.lock && idf.py reconfigure && idf.py build` exits 0 with zero new warnings. `idf.py size-components` shows factory partition ≥ 25 % free after the WS code adds ~10–15 KB. Capture the `size-components` evidence in the PR description.

## Out of scope (follow-ups, not part of this branch)

| Item | Why deferred | When it lands |
| --- | --- | --- |
| Producer/consumer split (capture task → depth-2 frame queue → stream task) | `cam_reader` mutex already serializes; current solution is sufficient for the chip's actual concurrency (1 messaging-core + occasional `/capture`) | When motion detection, ML inference, or a third long-lived consumer appears |
| `{"cmd":"stream","on":true,"fps":N}` text-frame control plane | Requires cJSON as a managed dep; useful for runtime FPS tuning but not needed in first cut | When the operator UI adds manual FPS / resolution controls |
| Multi-viewer policy (configurable `max_viewers`) | One WS client (messaging-core) is the actual use case | When go2rtc / a phone app needs simultaneous direct read |
| MJPEG-over-HTTP `/stream.mjpeg` endpoint for direct browser / VLC fallback | messaging-core owns fan-out; redundant on the chip | If go2rtc / Frigate integration goes around messaging-core |
| HTTP pre-handshake auth (`ws_pre_handshake_cb` with `token=valid`) | LAN isolation is the security boundary; both endpoints and the softAP are on the home wifi | When deploying outside the LAN or in a shared wifi |
| WebRTC / esp_webrtc_solution | Requires ESP32-S3 silicon (Espressif's own support matrix leaves ESP32-D0WD unconfirmed) | Fleet migration to ESP32-S3 |
| Frame-size / quality runtime adjustment | Build-time SVGA q12 today; the reference's `/api/settings` is heavier than we need | When operator asks |
| Host-test suite (Unity + `mock_esp_*` triplet) | iot_cams has zero host-side tests today; cam_reader's API was tested only by operator-flash | A separate test-infrastructure workstream; not part of this branch |

## Acceptance criteria

1. `idf.py build` exits 0 with no new warnings. ✅ verified at W1..W9.
2. `websocat ws://<device-ip>/ws/cams` performs the 101 upgrade, receives the JSON `hello` text frame, then BINARY JPEG frames at ≥ 10 Hz. Each frame decodes cleanly via `file`/image viewer; consecutive MD5 hashes are distinct. ✅ operator-flash.
3. `frames_sent` counter in the JSON `status` frame increments by ~10 per second of open session. ✅ operator-monitor.
4. Single-viewer: a second `websocat` while the first holds the stream receives `viewer_limit` text frame and the socket is closed; the first session is untouched. `/whoami` and `/capture` keep responding throughout. ✅ operator-flash.
5. Concurrent `curl /capture` while WS is streaming: `/capture` returns HTTP 503 (cam_reader mutex timeout) while the stream holds the mutex, then succeeds normally after the viewer closes; the WS session is uninterrupted. ✅ operator-flash.
6. Forced WiFi down/up while a viewer is connected: server logs `viewer disconnected (send failed)`, sink is cleared, the next `/ws/cams` handshake on the fresh IP succeeds without power cycle. Pre-fix: 404 forever. ✅ operator-flash.
7. Fleet parity: identical behavior on Class A (V3.1 + OV3660 + Boya, devices 1 and 3) and Class B (v1-marked + OV2640 + generic, device 2). No per-device sdkconfig split. ✅ operator-flash on all three.
8. NO-SOI warm-up: at WS-connect time the camera emits the documented warning density (engram 4334); warnings are non-continuous and `/ws/cams` frames are clean. ✅ operator-monitor reading.
9. Partition budget: factory partition ≥ 25 % free after WS adds ~10–15 KB. ✅ `idf.py size-components` evidence captured to PR description at W9.
10. TX mutex invariant: the JSON `status` timer fires while binary frames are streaming; both reach the wire as distinct WS messages (no interleaved bytes). ✅ operator-monitor reading.
11. Reconnect on viewer-close: when messaging-core drops (e.g. its own restart), the chip frees the slot within ≤ 1 frame period. The next handshake completes without reboot. ✅ operator-flash with `kill -9` on messaging-core.

## Build evidence (post-W9, manual gate)

```
$ rm -rf build dependencies.lock && idf.py reconfigure
... (4.1 s)
$ idf.py build
iot_cams.bin binary size 0xff0d0 bytes.
  Smallest app partition is 0x180000 bytes.
  0x80f30 bytes (34%) free.

Component sizes (idf.py size-components):
  libprovisioning.a   10514 B   (+ 1657 B over capture-endpoint)
  libcam_stream.a       991 B   (new component)
  libcam_reader.a       663 B   (+ 46 B for the augmented
                                  fb_drops / frames_captured
                                  counters)
  libmain.a            1174 B   (+108 B for cam_stream_init /
                                  cam_stream_task_start calls)
  libhttp_parser.a  ...     B
  libesp_http_server 13664 B  (was smaller pre-feature;
                              + CONFIG_HTTPD_WS_SUPPORT=y
                              pulls in the WS protocol stack)

Delta vs post-/capture baseline 0xfc2e0: +0x2df0 bytes (~11.7 KB)
  for the entire /ws/cams endpoint + the WS protocol stack.
Factory partition headroom: 34% free, well above the 25% floor.
```

## Commit log (8 work-unit commits, no squash yet)

```
ec81dac feat(iot-cams): sdkconfig knobs for ws-cams endpoint      (W8)
ef80f04 fix(wifi): re-attach /ws/cams on sta reconnect; clear    (W7)
9338201 feat(cam-stream): single-viewer policy with viewer_limit   (W6)
83a7e02 feat(cam-stream): bind ws sink seam to stream task ...    (W5)
6f3b484 feat(cam-stream): json text-frame builders (hello+status) (W3)
fccb522 feat(cam-stream): capture-send-release loop + stream task (W2)
b6bbd85 feat(cam-stream): port cam_stream component skeleton      (W1)
fa4b87e docs(odd): ws-cams-endpoint task plan                       (W0)
```

Final squash to a single commit will land when the operator
flashes the fleet and approves the PR.

## Tracking

Mirrored to Engram under project `local-home-assitant`, topic key
`iot_cams/ws-cams-endpoint`. `todo` list reflects this plan.
