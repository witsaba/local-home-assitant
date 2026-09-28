# components/cam_reader/CMakeLists.txt
#
# Registers the camera-reader component with the IDF component
# graph. PRIV_REQUIRES mirrors the reference project
# (../rural_home_assistant/backend/iot-camera/components/cam_reader/):
#   - esp32-camera — the OV2640 sensor driver (espressif/esp32-camera
#                    managed component, declared in idf_component.yml)
#   - esp_psram    — needed at boot to drive fb_count / fb_location
#   - nvs_flash    — defensive; the sensor driver may route state
#                    through NVS
idf_component_register(
    SRCS "cam_reader.c"
    INCLUDE_DIRS "include"
    PRIV_REQUIRES nvs_flash esp_psram esp32-camera
)
