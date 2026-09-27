# WiFi Provisioning for `iot_cams`

A self-contained WiFi provisioning package for the `iot_cams`
firmware. Brings up a SoftAP, runs an httpd, and serves an
HTML form the operator can fill in a phone browser to attach
the device to their home Wi-Fi. No apps to install, no
protocomm handshake, no internet access required during
deployment.

**Scope.** SoftAP-only operator flow via a captive HTML form.
The package talks to `esp_wifi` directly; the IDF
`wifi_provisioning` / `protocomm` manager is intentionally
not linked (see [Why no protocomm](#why-no-protocomm) below).

---

## Public surface

`include/provisioning.h` exposes five top-level functions plus a
small inter-module helper that the captive portal's HTTP handler
calls when the operator submits the form:

```c
esp_err_t provisioning_init(const provisioning_config_t *cfg,
                            const provisioning_app_info_t *app_info);

bool      provisioning_is_provisioned(void);

esp_err_t provisioning_run(void);                  /* blocking */

void      provisioning_stop(void);                 /* unblock */

esp_err_t provisioning_reset_credentials(void);    /* wipe NVS */

esp_err_t provisioning_apply_captive_form(const char *ssid,
                                         const char *password); /* inter-module */
```

The header documents every error path. Application code includes
only `provisioning.h`; the captive HTML form lives at the bottom of
this README as a sibling-style flow.

---

## Operator procedure (the only flow that matters)

1. **Power on the device.** On first boot (or after a factory
   reset) no Wi-Fi credentials are in NVS, so the package brings
   up a softAP at `IoT-Cam_3091B0` (the suffix is the last 3 bytes
   of the device's MAC).
2. **From a phone, join the softAP** using the WPA2 passphrase
   from `CONFIG_PROVISIONING_SOFTAP_PASS`. The operator sees a
   captive-portal prompt from iOS/Android the moment the
   connection completes; if not (some Androids don't pop it
   without an actual probe URL), the operator navigates
   manually to `http://192.168.4.1/`.
3. **The HTML form appears.** The header carries the device
   name and a `WPA2` badge (aria-label). A short paragraph above
   the form reminds the operator that the device only supports
   **2.4 GHz** so they don't sit at an empty network list on a
   band-steering 5 GHz-only router. The network dropdown is
   auto-populated from `/scan`, sorted by RSSI (best signal
   first) and deduped across mesh APs that share an SSID.
   Below the dropdown is a "Add hidden network manually"
   disclosure (`<details>`) for hidden SSIDs. Hidden networks
   are filtered out of the scan by the scanner so this is the
   only path to a hidden AP. `Rescan` re-runs `/scan` (also
   wired to the auto-scan on page load; the `Rescan` button is
   a manual override).
4. **The operator picks the home network and enters the WPA2
   passphrase** (8–63 ASCII chars). The form has no native
   JavaScript fetches — submission is a native browser POST
   to `/provision`, matching the existing C handler's
   `application/x-www-form-urlencoded` contract. While the
   submit is in flight the primary button stays disabled and
   its label flips to `Connecting...` so the operator knows
   the form took.
5. **The device persists those credentials** via
   `esp_wifi_set_config()` and `provisioning_run()` returns. The
   softAP stays up briefly so the operator's browser gets the
   "Connected" confirmation; the application body that comes
   after `provisioning_run()` drops the AP and the device
   continues as a station.

**Open networks.** Not supported. `provisioning_apply_captive_form`
hardcodes `WIFI_AUTH_WPA2_PSK` as the station threshold and
`esp_wifi_set_config` will not connect to an open AP under that
threshold. The `password` input is therefore `required`.

**Default credentials on the workbench.** SSID = `IoT-Cam_3091B0`,
WPA2 passphrase = `abcd1234`. The `abcd1234` placeholder is in
`CONFIG_PROVISIONING_SOFTAP_PASS`; ship-time packaging must replace
both with per-fleet values.

### Page layout

```
+---------------------------------------------+
| IoT-Cam-A1B2                          [WPA2]|
| Wi-Fi setup                                  |
+---------------------------------------------+
| Pick your home network. This device only    |
| supports 2.4 GHz - if your router shows two  |
| SSIDs, choose the 2.4 GHz one.              |
+---------------------------------------------+
| Network                                      |
|   [BestNetwork |||| -45 dBm        v]        |
|   [OtherNetwork |||  -58 dBm         ]       |
|   [Neighbor-Net |    -72 dBm         ]       |
|   [- Hidden (type above) -           ]       |
|   + Add hidden network manually              |
|                                              |
| Password                                     |
|   [..............................]           |
+---------------------------------------------+
|        [ Rescan ]    [      Connect      ]   |
+---------------------------------------------+
```

### UX changes from the previous iteration

| Change | Why |
| --- | --- |
| Submit is a native POST (no `fetch`) | One less JS round-trip; C handler unchanged |
| Submit button shows `Connecting...` until navigation | Operator no longer wonders if the form took |
| `aria-live='assertive'` error / `polite` success | Screen readers announce status changes |
| `<details>` for hidden SSID | Pure HTML disclosure, no JS toggle |
| Dropdown sorted by RSSI, mesh SSIDs deduped | Best signal at top of list; no duplicated entries |
| `2.4 GHz only` warning above the form | Stops band-steering routers from looking broken |
| WPA2 badge in the header with `aria-label` | Trust signal without a decorative SVG byte cost |
| Dark mode via `prefers-color-scheme` | Operator configures at night |
| `prefers-reduced-motion` honored | No animation regression if we add transitions later |
| `:user-invalid` styled with `--e` | Browser-native flag for invalid fields without JS |
| Device name substituted into `<title>` and `<h1>` | Operator knows which device they're configuring |
| Verbose `ESP_LOGI`/`ESP_LOGW` in this file gated with `#if 0` | Flash budget for the log format strings |

---

## Bring-up sequence (firmware internals)

```
app_main
  ├── provisioning_init(cfg, info)
  │     ├── nvs_flash_init() (idempotent on repeat calls)
  │     └── store service_name + cfg
  ├── if (provisioning_is_provisioned())  → return ESP_OK
  └── provisioning_run()  ── blocking
        ├── softap_bring_up()
        │     ├── esp_netif_init  (idempotent)
        │     ├── esp_event_loop_create_default  (idempotent)
        │     ├── esp_netif_create_default_wifi_ap  (BEFORE esp_wifi_init)
        │     ├── esp_netif_set_default_netif
        │     ├── esp_wifi_init
        │     ├── esp_wifi_set_mode(WIFI_MODE_APSTA)
        │     ├── esp_wifi_start
        │     ├── mdns_init
        │     └── mdns_hostname_set
        ├── captive_portal_bring_up()  ── httpd on port 80
        │     ├── httpd_start(&server, HTTPD_DEFAULT_CONFIG)
        │     ├── httpd_register_uri_handler(/, GET)
        │     ├── httpd_register_uri_handler(/provision, POST)
        │     ├── httpd_register_uri_handler(/whoami, GET)
        │     └── httpd_register_err_handler(HTTPD_404_NOT_FOUND, ...)
        ├── xSemaphoreCreateBinary()  ── the unblock primitive
        ├── xSemaphoreTake(...)  ── blocks on this thread
        │
        │  … operator browses to 192.168.4.1/ and submits the form …
        │  captive_portal POST /provision parses form-urlencoded
        │  body and calls provisioning_apply_captive_form(ssid, password)
        │  which calls esp_wifi_set_config(WIFI_IF_STA) and signals
        │  the semaphore.
        │
        ├── xSemaphoreTake returns
        ├── captive_portal_tear_down()  ── httpd_stop
        └── return ESP_OK (or ESP_FAIL if provisioning_stop was called)
```

`esp_netif_create_default_wifi_ap` MUST run before `esp_wifi_init`
in IDF v5.5.x — IDF returns `ESP_ERR_INVALID_STATE` if the order
is reversed.

---

## Why no protocomm

The project originally wrapped IDF's `wifi_provisioning` manager
with the SoftAP scheme. That requires the operator to install the
Espressif "ESP SoftAP Provisioning" phone app on their phone.
The current deployment environment is offline / private-network
only, so the operator cannot reliably download the app at install
time.

The captive-portal HTML form runs entirely off
`httpd_register_uri_handler` + `esp_wifi_set_config`. The WPA2
passphrase on the softAP link is the operator-visible security
boundary; the security-1 PoP from the original IDF path is
accepted on the `provisioning_init()` arg for API stability but
ignored at runtime.

Removing the IDF manager also dropped the shared-httpd
`wifi_prov_scheme_softap_set_httpd_handle()` codepath, which
LoadProhibited-crashed in `httpd_find_uri_handler` during
the on-device verification pass. The simpler direct-API path
side-steps that bug.

---

## Security model

| Setting | Default | Where it lives |
| --- | --- | --- |
| Transport | SoftAP + HTTP + mDNS | inline `esp_wifi` + `esp_http_server` |
| SoftAP WPA2 passphrase | `CONFIG_PROVISIONING_SOFTAP_PASS` (8–64 bytes) | Kconfig |
| Service name | `"{prefix}_{MAC3}"` | derived in `derive_service_name()` |
| Captive-form credentials (operator-typed) | run-time, no secret persisted on the device | `esp_wifi_set_config()` writes them to NVS |

The WPA2 passphrase on the softAP is the only operator-visible
security boundary in the captive-portal flow. It must be printed
on the device label or in the install guide. The original
security-1 PoP code path was removed when the IDF manager was
dropped; the Kconfig `CONFIG_PROVISIONING_POP` is preserved for
API compatibility but ignored at runtime.

For deployments in hostile networks (or any place the operator's
phone cannot trust the softAP), the package should grow a
`wifi_prov_scheme_softap`-based flow alongside the captive path
(see [Extending](#extending)).

---

## Persistence

The package does **not** carry a parallel NVS namespace. The IDF
manager writes ssid + password to esp_wifi's NVS storage during
the `apply_config` flow; we read back through
`wifi_prov_mgr_is_provisioned()` and reset through
`wifi_prov_mgr_reset_provisioning()`.

| Key | Owner | Cleared by |
| --- | --- | --- |
| `nvs.net80211` / station ssid + password | esp_wifi (via `wifi_prov_mgr_*`) | `provisioning_reset_credentials()` |
| PoP, service_name, security_level | Kconfig at build time | none (firmware upgrade path) |

---

## Custom endpoint: `/whoami`

Registered alongside the captive portal on the same httpd server
(see [Bring-up sequence](#bring-up-sequence-firmware-internals)).

`GET /whoami` returns the device's identity as JSON. The
captive-portal flow replaces the previous (protocomm-based)
`iot-cam-info` endpoint with this httpd-served version; nothing
about the response shape changed.

Request: none (empty body).
Response:

```json
{
  "name": "iot-cam",
  "fw_version": "0.1.0",
  "softap_ssid": "IoT-Cam_3091B0",
  "softap_security": "WIFI_AUTH_WPA2_PSK"
}
```

---

## Menuconfig

```
WiFi Provisioning  --->
  (abcd1234) Default proof-of-possession
  (abcd1234) SoftAP WPA2 passphrase
  (IoT-Cam) SoftAP SSID prefix
  (iot-cam) Device name exposed on the iot-cam-info endpoint
```

> The `PoP` symbol is preserved for API compatibility but is
> ignored at runtime in captive-portal mode — the WPA2
> passphrase is the operator-visible security gate.

`idf.py menuconfig` shows the menu after the package is
added to the build graph (path dependency resolves during
`idf.py reconfigure`).

---

## Factory-reset

Wipe the device by:

```c
provisioning_reset_credentials();   /* calls esp_wifi_restore() */
esp_restart();
```

Both calls live in the firmware; the package owns the reset path
only. Wiring this to a GPIO button (or another trigger) is
follow-up work — the package exposes the function so any caller
can wire it.

---

## Extending

| Need | Where to put it |
| --- | --- |
| Camera + capture + ws plane | a sibling component in `embedded/iot_cams/components/camera/`, etc. |
| BLE transport | reintroduce protocomm + wifi_provisioning as REQUIRES and write a separate scheme layer (out of scope for the offline-deployment branch). |
| Factory-reset GPIO | any caller; this package exposes the reset function, not the trigger. |
| True captive-portal OS-detection | standalone DNS server on the softAP that resolves all hostnames to 192.168.4.1; this is a meaningful follow-up, see [Out of scope](#out-of-scope-for-this-branch). |

---

## Dependencies (managed vs built-in)

| Component | Source | Why |
| --- | --- | --- |
| `provisioning`, `iot-cam-info` | this package | — |
| `wifi_provisioning`, `protocomm`, `esp_wifi`, `esp_netif`, `esp_event`, `nvs_flash` | built-in to ESP-IDF v5.5 | declared in `CMakeLists.txt` `REQUIRES` |
| `mdns` | **managed**, `espressif/mdns ^1.0.0` | moved out of the IDF tree in v5.5; declared here in `idf_component.yml::dependencies` |

The `mdns` dep is the only one that's not a built-in. Without it,
ESP-IDF v5.5.x's component manager fails with
`Failed to resolve component 'mdns' required by component 'provisioning'`
at `idf.py build` time. The lock file is frozen per consumer by
`idf.py reconfigure`; removing the dep locally requires updating
the lock in lockstep.

After the captive rewrite, `protocomm` and `wifi_provisioning`
were DROPPED from this list — the captive flow doesn't use either.

---

## Lifting to a managed component

The component is currently path-vendored from
`embedded/iot_cams/components/provisioning/`. To promote it to a
real upstream (registry, separate git repo, or private URL):

1. Move `embedded/iot_cams/components/provisioning/` to its own
   repo. Tag a release.
2. Edit `embedded/iot_cams/components/provisioning/idf_component.yml`
   `url:` to the upstream URL.
3. Edit `embedded/iot_cams/main/idf_component.yml` — replace
   `path: ../components/provisioning` with the new URL.

No header or implementation changes are needed. The repo lifts
without touching the firmware source.

---

## Out of scope for this branch

- Camera pipeline + WebSocket control plane + supervision tasks.
- BLE transport (see Extending table).
- True captive-portal OS auto-detection via a DNS server on the
  softAP. iOS/Android's "Sign in to network" prompt reads from
  `captive.apple.com` / `connectivitycheck.gstatic.com`, so
  without a DNS responder that resolves those to 192.168.4.1,
  the OS-prompt trick doesn't fire automatically. Today the
  operator must type `http://192.168.4.1/` explicitly (or follow
  the OS prompt if their device supports it). A LwIP-level DNS
  responder is the next milestone for full zero-touch UX.
- Host-side UNITY test bed inside `iot_cams/tests/`.
- TLS over the captive httpd. The WPA2 link is encrypted; for
  hostile environments the operator would need to switch to the
  protocomm path (see Extending table).
