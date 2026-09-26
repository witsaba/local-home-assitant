# Feature: WiFi provisioning package for `embedded/iot_cams`

## Goal

Replace the hand-rolled softAP provisioning inside
`esp32-cam-surveillance/firmware/components/softap/` with a clean,
self-contained, ESP-IDF-canonical provisioning package targeted at the
`@embedded/iot_cams/` project. The new package wraps
`wifi_provisioning` (v5.5.4) with a strict public surface so
`iot_cams` `app_main()` only has to ask "is provisioned?" and, if
not, call `provisioning_run()` — everything else is owned by the
package.

## Design decisions (locked with the user)

| Decision | Choice | Rationale |
| --- | --- | --- |
| Transport | **SoftAP + HTTP + mDNS** (`wifi_prov_scheme_softap`) | Matches existing camera firmware shape; no BLE stack cost; works on every ESP32-CAM board. |
| Layout | **Local component + path dep** declared in `main/idf_component.yml` | Component materializes through `idf.py reconfigure`; one-line URL flip later lifts it to a real managed component / separate repo. |
| Source location | `embedded/iot_cams/components/provisioning/` | Owned by the iot_cams project but distributable through a re-route. |
| Security | **Security 1** (X25519 + AES-CTR) | Matches Espressif production guidance; demo PoP in `g_demo_pop` for the bring-up, surfaced via menuconfig. |
| Persistence | Package owns its own NVS namespace `prov_cfg`; does not introduce a global `config` component yet | Keeps the package self-contained; future iot_cams config component can read from this namespace without rewriting the package. |

## Public API (header target)

```c
/* provisioning.h */
typedef struct {
    bool     force_provisioning;          /* skip is_provisioned check */
    uint8_t  pop[PROV_POP_MAX_LEN + 1];   /* proof-of-possession */
    size_t   pop_len;
    char     service_name[PROV_NAME_MAX_LEN + 1];   /* softAP SSID */
} provisioning_config_t;

esp_err_t provisioning_init(const provisioning_config_t *cfg);
bool      provisioning_is_provisioned(void);
esp_err_t provisioning_run(void);                       /* blocking */
void      provisioning_stop(void);                     /* for forced re-provision */
esp_err_t provisioning_reset_credentials(void);        /* wipe NVS */
```

The package emits a small set of named log events matching `WIFI_PROV_*`
for greppability.

## Tasks

- [ ] **T1 — Scaffold provisioning component skeleton**
  - `components/provisioning/CMakeLists.txt`
  - `components/provisioning/idf_component.yml`
  - `components/provisioning/include/provisioning.h` — public API
  - `components/provisioning/Kconfig.projbuild` — service-name + PoP menuconfig
  - Empty stubs in `src/provisioning.c` so the file groups compile.
  - Work-unit commit: `feat(embedded): scaffold provisioning component skeleton`

- [ ] **T2 — Provisioning core (init / is_provisioned / run / stop)**
  - `wifi_prov_mgr_init(WIFI_PROV_SCHEME_SOFTAP, WIFI_PROV_SCHEME_SOFTAP_EVENT_HANDLER_NONE, ...)`.
  - Event handler for `WIFI_PROV_*` translating to our log lines.
  - `provisioning_run()` blocks on `wifi_prov_mgr_wait()` and returns on `WIFI_PROV_END`.
  - `mDNS` init before `wifi_prov_mgr_start_provisioning()`.
  - Stop path: `wifi_prov_mgr_stop_provisioning()` then `wifi_prov_mgr_deinit()`.
  - `provisioning_is_provisioned()` reads from the same NVS namespace the manager uses.
  - Work-unit commit: `feat(provisioning): implement provisioning core wrappers`.

- [ ] **T3 — Credentials persistence + reset**
  - Document the storage layout: `wifi_prov_mgr_*` persists `ssid` + `password`
    to NVS via `esp_wifi_set_config()` — verified by reading the v5.5
    manager header (`wifi_provisioning/manager.h`). The package does **not**
    introduce a parallel `prov_cfg` namespace; that would duplicate state.
  - `provisioning_reset_credentials()` — single function, single path:
    `wifi_prov_mgr_reset_provisioning()` clears the wifi-managed keys.
    `wifi_prov_mgr_is_provisioned()` then returns false on the next call,
    and the next `provisioning_run()` brings up the SoftAP.
  - PoP / service_name are either Kconfig-fixed at build time or supplied
    per-boot via `provisioning_config_t` — neither needs runtime NVS
    persistence. Document this so a future maintainer does not "helpfully"
    add a parallel namespace.
  - Work-unit commit: `feat(provisioning): document storage layout + reset path`.

- [ ] **T4 — Custom endpoint: `iot-cam-info`**
  - Register an additional protocomm endpoint exposing device identity (name, fw version).
  - Endpoint handler is REGISTER'd AFTER `start_provisioning()` and UNREGISTER'd on deinit (per IDF pattern).
  - Handler reads its data via a `provisioning_app_info_t` registered at init.
  - Work-unit commit: `feat(provisioning): expose iot-cam-info custom endpoint`.

- [ ] **T5 — Wire into `iot_cams` main firmware**
  - Update `embedded/iot_cams/main/idf_component.yml` to declare the local path dep.
  - Update `embedded/iot_cams/main/CMakeLists.txt` to add `provisioning` to `REQUIRES`.
  - Update `embedded/iot_cams/main/iot_cams.c` to call into the new package — `provisioning_init` + loop on `!provisioning_is_provisioned` then `provisioning_run`.
  - Work-unit commit: `feat(iot-cams): wire provisioning package into app_main`.

- [ ] **T6 — Documentation**
  - `components/provisioning/README.md`: flow, security model, menuconfig reference, how to reset, how to lift to a managed repo.
  - Component header docs cross-link to the README.
  - Work-unit commit: `docs(provisioning): add component README + flow notes`.

- [ ] **T7 — Final integration smoke**
  - Local `cmake --version` availability check (the docker/cross compile is not available in this sandbox; we validate by reading-compiling the headers, checking the project's `idf.py` configuration is consistent, and reviewing the linker surface).
  - Work-unit commit: `chore(provisioning): final integration review notes`.

## Out of scope (follow-ups, not part of this branch)

- A full `config` component with `identity`, `camera`, `control` fields.
- BLE transport (`wifi_prov_scheme_ble`).
- Captive portal / custom HTML form.
- Host-side UNITY test bed inside `iot_cams/tests/`.
- Factory-reset GPIO button contract; the package only exposes
  `provisioning_reset_credentials()` so the caller can wire any reset trigger.

## Acceptance criteria

1. The component compiles when added to a fresh ESP-IDF v5.5 project
   that only enables `esp_wifi`, `esp_netif`, `esp_http_server`,
   `protocomm`, `wifi_provisioning`, `mdns`, `nvs_flash`, `esp_event`.
2. `app_main` body fits inside 20 lines and contains no IDF-specific
   includes beyond `provisioning.h`.
3. README documents the bring-up sequence, the protocol endpoint map,
   the NVS namespace, and the path-to-managed upgrade.
4. No hand-rolled `httpd` calls remain inside the provisioning code
   path — all HTTP lives inside the IDF `protocomm_httpd` server.

## Tracking

Mirrored to Engram under project `local-home-assitant` topic
`iot_cams/wifi-provisioning`. `todo` list reflects this plan.
