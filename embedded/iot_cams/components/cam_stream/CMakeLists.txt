# components/cam_stream/CMakeLists.txt
#
# Registers the camera-stream consumer component with the IDF
# component graph.
#
# REQUIRES is intentionally empty in W1: the public header
# (`include/cam_stream.h`) exposes no types from sibling
# components — `cam_stream_sink_t` is built from `esp_err.h`,
# `<stddef.h>`, `<stdbool.h>`, `<stdint.h>`, all IDF core
# headers. Downstream consumers that include only
# `cam_stream.h` need no transitive component on the include
# path, per engram 4331 ("if a component's public header
# references a type from another component, that other
# component must be in REQUIRES").
#
# PRIV_REQUIRES captures the private linkage the implementation
# will need as W2..W5 land:
#   - cam_reader    — cam_stream.c calls cam_reader_capture /
#                     cam_reader_release; cam_reader's public
#                     header is consumed internally only, so a
#                     PRIV edge is correct.
#   - esp_timer     — future status-timer + loop FPS metering
#                     (W8); pulled in lazily by cam_reader's
#                     own dep today.
#   - freertos      — xSemaphoreCreateMutex() (W5 TX mutex),
#                     xTaskCreate() (W2 stream task). The IDF
#                     build resolves freertos symbols
#                     automatically when an app is built, but
#                     the explicit declaration matches the IDF
#                     hygiene pitfalls captured in engram 4331.
idf_component_register(
    SRCS "cam_stream.c"
         "cam_stream_sender.c"
         "cam_stream_wire.c"
    INCLUDE_DIRS "include"
    PRIV_REQUIRES cam_reader esp_timer freertos
)
