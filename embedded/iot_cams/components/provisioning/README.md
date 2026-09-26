# WiFi Provisioning for `iot_cams`

A thin wrapper around ESP-IDF v5.5's `wifi_provisioning` manager.
The package hides `protocomm`, `mdns`, `esp_wifi`, `esp_netif`,
and `nvs_flash` from the rest of the firmware so `app_main` is
three lines long.

**Scope.** SoftAP transport only. Security 1 only (X25519 + PoP +
AES-CTR). BLE transport and Security 2 are deliberately out of
scope — see [Extending](#extending) below.

---

## Public surface

`include/provisioning.h` exposes exactly five functions plus one
struct pair:

```c
esp_err_t provisioning_init(const provisioning_config_t *cfg,
                            const provisioning_app_info_t *app_info);

bool      provisioning_is_provisioned(void);

esp_err_t provisioning_run(void);                  /* blocking */

void      provisioning_stop(void);                 /* trigger teardown */

esp_err_t provisioning_reset_credentials(void);    /* wipe NVS */
```

The header documents every error path. Application code includes
only this file; every `esp_wifi_*` / `protocomm_*` symbol lives
inside the package.

---

## Bring-up flow

```
app_main
  ├── provisioning_init(cfg, info)
  ├── if (provisioning_is_provisioned())  → join AP, return
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
        ├── wifi_prov_mgr_init(WIFI_PROV_SCHEME_SOFTAP, ...)
        ├── esp_event_handler_register(WIFI_PROV_EVENT, ...)
        ├── wifi_prov_mgr_endpoint_create("iot-cam-info")
        ├── wifi_prov_mgr_start_provisioning(SECURITY_1, pop, ssid, "")
        ├── wifi_prov_mgr_endpoint_register("iot-cam-info", handler, NULL)
        └── wifi_prov_mgr_wait()  ── blocks until WIFI_PROV_END
```

The ordering of `esp_netif_create_default_wifi_ap` relative to
`esp_wifi_init` is intentional — IDF v5.5.3 returns
`ESP_ERR_INVALID_STATE` if the order is reversed. The pattern
above is the same one `examples/provisioning/wifi_prov_mgr` in
the IDF tree uses.

---

## Security model

| Setting | Default | Where it lives |
| --- | --- | --- |
| Transport | SoftAP + HTTP + mDNS | `wifi_prov_scheme_softap` |
| Security | Security 1 (X25519 + PoP + AES-CTR) | Kconfig + per-call `cfg.security` |
| PoP | `CONFIG_PROVISIONING_POP` | Kconfig |
| SoftAP WPA2 passphrase | `CONFIG_PROVISIONING_SOFTAP_PASS` (8–64 bytes) | Kconfig |
| Service name | `"{prefix}_{MAC3}"` | derived in `derive_service_name()` |

Security 0 (plain text) is exposed for debugging; never ship
firmware with it enabled. Security 2 (SRP6a + AES-GCM) requires a
managed-component extension and is intentionally out of scope.

The phone-side provisioning app prompts the operator for the PoP
during the security-1 handshake — make sure the PoP is printed on
the device's label or in its installation guide.

The phone first joins the device's softAP network (SSID
`IoT-Cam_xxXXxx`, WPA2 passphrase from `CONFIG_PROVISIONING_SOFTAP_PASS`),
then receives the security-1 challenge, then enters the PoP. The
two credentials are separate on purpose — the passphrase gates the
Wi-Fi link; the PoP gates the secure provisioning session on top.

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

## Custom endpoint: `iot-cam-info`

Registered after `start_provisioning()` and unregistered
automatically when the manager stops. Replaces the `/whoami` URL
the previous hand-rolled softAP firmware exposed.

Request: none (empty body).
Response:

```json
{
  "name": "iot-cam",
  "fw_version": "0.1.0"
}
```

Missing fields fall through to empty strings rather than
`\0` stringification glitches. Response buffer is `malloc`'d in
the handler; protocomm frees it.

---

## Menuconfig

```
WiFi Provisioning  --->
  (abcd1234) Default proof-of-possession
  (abcd1234) SoftAP WPA2 passphrase
  (IoT-Cam) SoftAP SSID prefix
  (iot-cam) Device name exposed on the iot-cam-info endpoint
```

`idf.py menuconfig` shows the menu after the package is
added to the build graph (path dependency resolves during
`idf.py reconfigure`).

---

## Factory-reset

Wipe the device by:

```c
provisioning_reset_credentials();
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
| BLE transport | a sibling component or fork the package; the SoftAP-only choice is documented in `include/provisioning.h`. |
| Factory-reset GPIO | any caller; this package exposes the reset function, not the trigger. |
| Custom provisioning flows (e.g., cloud handoff) | a custom protocomm endpoint registered via `wifi_prov_mgr_endpoint_register()` after `start_provisioning()`. |

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
- HTTP captive portal / phone-app UI work.
- Host-side UNITY test bed inside `iot_cams/tests/`.
