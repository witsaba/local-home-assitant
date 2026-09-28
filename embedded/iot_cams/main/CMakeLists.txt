# embedded/iot_cams/main/CMakeLists.txt
#
# Registers the iot_cams app entry-point component with the IDF
# component graph. REQUIRES `provisioning` (the WiFi + httpd path
# package) and `cam_reader` (the OV2640 sensor + camera-mutex
# component) so the linker sees the symbols from each.
idf_component_register(
    SRCS "iot_cams.c"
    INCLUDE_DIRS "."
    REQUIRES provisioning cam_reader cam_stream
)
