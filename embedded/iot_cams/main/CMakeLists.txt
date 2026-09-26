# embedded/iot_cams/main/CMakeLists.txt
#
# Registers the iot_cams app entry-point component with the IDF
# component graph. REQUIRES `provisioning` (the path-dep in
# idf_component.yml adds the local component to the graph) so the
# linker sees our symbols.
#
# Future grown-up REQUIRES list will grow to include the camera
# pipeline, the WebSocket control plane, and the supervision
# tasks once those components land.
idf_component_register(
    SRCS "iot_cams.c"
    INCLUDE_DIRS "."
    REQUIRES provisioning
)
