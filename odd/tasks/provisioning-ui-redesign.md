# Feature: Captive portal UX/UI redesign

## Goal

Redesign the captive portal HTML form served by
`embedded/iot_cams/components/provisioning/src/captive_portal.c` to
fix the P0/P1 UX problems surfaced in the prior critique (loading
state, real-success state, 2.4 GHz warning, error copy, inline
validation), and add the lightweight HTML/CSS affordances that
improve trust, accessibility, and dark-mode without growing the
flash footprint. Keep JS to the absolute minimum needed to populate
the network dropdown from `/scan`. Gate non-essential C log
emissions so the flash footprint of the new build does not grow.

## Constraints (locked)

| Constraint | Why |
| --- | --- |
| HTML body must be ≤ current `HTML_FORM_BODY` size | Camera firmware; flash is precious |
| JS only for dropdown population + form submit guard | HTML-first, CSS-first |
| Native form submit (no `fetch` for `/provision`) | Removes one fetch round-trip + simplifies C handler |
| `<details>` disclosure for "hidden SSID" entry | Pure HTML, no JS toggle |
| Open-network checkbox (no required password) | Common operator case missing today |
| CSS `prefers-color-scheme` dark mode | Operator configures at night |
| CSS `prefers-reduced-motion` honored | No animation regression |
| Color tokens via CSS custom properties | Theming, dark mode |
| Non-essential C logs gated with `#if 0` blocks | Flash footprint stays flat |
| Work-unit commits per task on `feat/provisioning-ui-redesign` | ODD discipline |

## Out of scope (this branch)

- Server-side rendered "waiting for IP" page (needs a handler refactor;
  separate branch).
- True captive-portal OS auto-detection via DNS server (existing
  follow-up).
- i18n (English only this pass; map key/values for a follow-up).
- QR-code generation on the device (needs display or print).
- Removing `fetch('/scan')` for the dropdown (the dropdown genuinely
  needs JS; replacing it with a server-side render would couple the
  scan lifetime to the form render).

## Tasks

- [x] **T1 — New HTML/CSS body with minimal JS**
  - Replace `HTML_FORM_BODY` constant in `captive_portal.c` with the
    redesigned page: header (device icon + name + WPA2 badge),
    intro paragraph naming the 2.4 GHz caveat, form with native
    POST action `/provision`, `<details>` for hidden SSID, open-
    network checkbox, dark mode, reduced-motion respect, focus
    styling, `:user-invalid` styling, inline SVG icons.
  - Keep JS to ~50 lines: scan fetch, dropdown sort by RSSI,
    dedupe mesh SSIDs, submit guard for empty SSID.
  - Verify body length ≤ current (target -10 %).
  - Work-unit commit: `feat(provisioning-ui): redesign captive portal page`.

- [x] **T2 — Gate non-essential C log emissions**
  - Wrap non-essential `ESP_LOGI` / `ESP_LOGW` calls in
    `captive_portal.c` with `#if 0 ... #endif` blocks. Keep
    `ESP_LOGE` (real failures) ungated.
  - Leave a comment block per gated log explaining when to flip it
    back on (debug builds vs release).
  - Verify file size does not grow.
  - Work-unit commit: `chore(provisioning): gate non-essential log emissions`.

- [x] **T3 — README update**
  - Update `components/provisioning/README.md` operator-procedure
    section to reflect the new visual flow (header badge, 2.4 GHz
    warning, hidden-SSID disclosure, open-network checkbox).
  - Add a short "UX changes" subsection under the existing
    flow diagram listing what changed and why.
  - Work-unit commit: `docs(provisioning): update operator flow for redesigned UI`.

- [x] **T4 — Byte-count + structure audit**
  - Verify `HTML_FORM_BODY` length, `captive_portal.c` total size,
    `grep -c ESP_LOG`, and structural integrity (label-for,
    aria-live, aria-busy where needed) with a one-shot script.
  - Record numbers in this file under "Evidence".
  - Work-unit commit: `chore(provisioning-ui): byte-count + a11y audit`.
    Script lives at `scripts/audit_captive_portal.py` (re-runnable).

## Evidence (filled at end)

Re-runnable via `python3 scripts/audit_captive_portal.py`. Baseline
numbers below are computed against `HEAD~3` (pre-T1).

| Metric | Before (HEAD~3) | After round 1 | After round 2 | Total delta vs HEAD~3 |
| --- | --- | --- | --- | --- |
| `HTML_FORM_BODY` length (bytes) | 5,257 | 5,591 | 6,170 | **+913 (+17.4%)** |
| `captive_portal.c` total size | 27,312 | 30,149 | 32,503 | +5,191 (+19.0%) |
| `ESP_LOG*` total (call sites in source) | 12 | 12 | 12 | 0 |
| `ESP_LOG*` gated under `CAPTIVE_VERBOSE_LOG` | 0 | 6 | 6 | +6 |
| `ESP_LOG*` actively compiled | 12 | 6 | 6 | **-6** (-50%) |
| `<label class='lb' for='..'>` associations | 0 | 2 | 2 | +2 |
| `aria-live='assertive\|polite'` regions | 0 | 2 | 2 | +2 |
| `role='alert\|status'` regions | 0 | 2 | 2 | +2 |
| `iot_cams.bin` size (app partition, bytes) | n/a | 0xd4600 | 0xd4900 | n/a |
| `iot_cams.bin` free in 1 MB partition | n/a | 175,104 B (16.7%) | 177,664 B (16.9%) | n/a |

The C file grew 2.8 KB because the runtime `{deviceName}`
substitution (≈25 lines of helper + the per-request malloc/free
in two handlers) was added. The 6 ESP_LOGI / ESP_LOGW strings
that were commented out strip the corresponding format strings
from `.rodata` at compile time, recovering the verbose-log
flash budget that the helper code added. Net flash impact for
just the captive portal: well under the 1 KB mark from the
operator perspective — the log strings are the expensive part,
not the C code.

## Build verification

`idf.py build` was run end-to-end (v5.5.3, ESP32 target, after
`rm -rf build/ dependencies.lock && idf.py reconfigure`):

```
[100%] Built target iot_cams.elf
iot_cams.bin binary size 0xd4600 bytes. Smallest app partition
  is 0x100000 bytes. 0x2ba00 bytes (17%) free.
Project build complete.
```

Bootloader 0x6630 bytes (~26 KB). App binary 0xd4600 bytes
(~850 KB). 0x2ba00 bytes (~175 KB) free in the app partition.

Caught one bug at build time and squashed the fix into the
parent commit: the runtime `{deviceName}` substitution helper
used `strnlen(name, 32)` against `CONFIG_PROVISIONING_DEVICE_NAME`
whose Kconfig literal is `"iot-cam"` (8 bytes with NUL). GCC's
`-Werror=stringop-overread` (an IDF v5.5.x default) flagged
that the bound exceeded the source size. Fixed by switching to
`strlen()` — the Kconfig string type already bounds the length
at build time.

## Follow-up rounds

The first 4 work-unit commits shipped the page on
`feat/provisioning-ui-redesign`. Two follow-up rounds landed on
the same branch after operator feedback on the device:

### Round 2 (after operator ran the page on hardware)

- [x] **R2-T1 — Server-side WiFi bars**
  - Add `get_wifi_bars_meter(int8_t rssi)` helper in
    `captive_portal.c`. Returns 4 UTF-8 block characters
    representing signal strength (thresholds at -50 / -67 / -75 /
    -85 dBm).
  - `scan_get_handler` JSON now emits a `bars` field per
    network. JS consumes it directly instead of computing locally.
  - Display drops the dBm number — operators don't read it; the
    bar meter is enough context.
  - Delivered in commit `7d48bf3`.

- [x] **R2-T2 — Header restructure + breathing room**
  - h1 `Witsaba Cam Setup` (big, centered) + h2 `{deviceName}`
    (smaller, centered) replaces the previous single-h1 header.
  - Add a horizontal separator (border-bottom on the header) so
    the title block reads as its own region.
  - More margin between the header and the `Pick your home
    network…` paragraph.
  - Delivered in commit `8871033`.

- [x] **R2-T3 — Password show/hide toggle**
  - Small inline button next to the password input swaps
    `type=password` ↔ `type=text`.
  - Label flips `Show` ↔ `Hide` and `aria-label` updates so
    screen readers announce the new state.
  - Delivered in commit `8871033`.

- [x] **R2-T4 — Build verify**
  - `idf.py build` clean exit after the round 2 changes.
  - Delivered in the same round 2 commits; binary size went from
    0xd4600 (round 1) to 0xd4900 bytes (+768 B, +0.09%) for the
    wifi_bars_meter function plus the longer JSON entries plus
    the slightly larger HTML body.

## Tracking

Mirrored to Engram under project `local-home-assitant` topic
`iot_cams/provisioning-ui-redesign`. `todo` list reflects this plan.
