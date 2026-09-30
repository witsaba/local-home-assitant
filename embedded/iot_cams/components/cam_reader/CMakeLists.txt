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
#   - esp_driver_ledc — owns the GPIO 4 flash LEDC channel (Timer 1 /
#                    Channel 1). Timer/Channel 0 is reserved for the
#                    camera XCLK; using Timer 1 here is the only safe
#                    slot that doesn't stall the camera.
idf_component_register(
    SRCS "cam_reader.c"
    INCLUDE_DIRS "include"
    # esp32-camera is in REQUIRES (not PRIV_REQUIRES) because
    # cam_reader.h exposes camera_fb_t* in its public surface;
    # any consumer that includes cam_reader.h transitively needs
    # the esp_camera.h include path.
    REQUIRES esp32-camera
    PRIV_REQUIRES nvs_flash esp_psram esp_timer esp_driver_ledc
)
