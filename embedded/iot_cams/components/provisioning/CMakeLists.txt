# components/provisioning/CMakeLists.txt
#
# Registers the iot_cams WiFi provisioning component. Wraps the
# IDF v5.5 `wifi_provisioning` component (SoftAP scheme, security 1)
# and exposes a strict 5-function public C API to the rest of the
# firmware so app_main never sees protocomm, mDNS or esp_wifi
# directly during provisioning.
#
# REQUIRES — pulls IDF's vendored components so the build graph is
# complete:
#   - esp_wifi / esp_netif / esp_event — base radio + TCP/IP stack
#     + event loop dependencies of wifi_provisioning
#   - nvs_flash  — credential persistence
#   - mdns       — required by SoftAP scheme for service discovery
#     (must be initialized before start_provisioning)
#   - protocomm  — underlies all wifi_provisioning security modes
#   - wifi_provisioning — the manager we wrap
#
# Sources include/provisioning.h is the only public surface; the
# linked IDF symbols live in the .a libraries IDF already builds.
idf_component_register(
    SRCS "src/provisioning.c"
    INCLUDE_DIRS "include"
    REQUIRES esp_wifi esp_netif esp_event nvs_flash mdns protocomm wifi_provisioning
)
