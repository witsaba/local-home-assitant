/* cam_stream_wire.c — W3 pure JSON text-frame builders.
 *
 * Two pure builders + a fixed caps array. No IDF runtime calls
 * inside this TU — callers pre-populate the snapshot structs
 * (`cam_stream_identity_t`, `cam_stream_status_metrics_t`) so
 * builders stay idempotent and host-testable.
 *
 * Field order in the rendered JSON is INVARIANT — mirrors the
 * REQ-WS-002 / REQ-WS-006 schemas in the esp32-cam-surveillance
 * reference; downstream parsers (messaging-core, future web
 * UI) must be able to rely on the shape byte-for-byte.
 *
 * Convention (same as the reference):
 *   - Builders return `size_t` (bytes written excluding NUL).
 *   - 0 is the sentinel for "no frame emitted" (buffer too
 *     small, NULL input, etc.) — caller skips the send on 0.
 *   - Out is always NUL-terminated on success (the NUL itself
 *     is not counted in the returned size, matching snprintf).
 */

#include <stddef.h>

#include <stdio.h>
#include <string.h>

#include "cam_stream.h"

/* Caps array is fixed; the chip offers jpeg (the sensor), stream
 * (this component), and identify (the /whoami endpoint, exposed
 * once a viewer asks). The control capability is reserved for
 * the {"cmd":"stream"} text-frame that lands in a follow-up
 * branch (see odd/tasks/ws-cams-endpoint.md "Out of scope"). */
static const char CAM_STREAM_CAPS_JSON[] =
    "\"caps\":[\"jpeg\",\"stream\",\"identify\"]";

size_t cam_stream_wire_build_hello(const cam_stream_identity_t *id,
                                     char *out, size_t out_len)
{
    if (id == NULL || out == NULL || out_len == 0) {
        return 0;
    }

    /* Format per REQ-WS-002; field order is invariant. */
    int n = snprintf(out, out_len,
        "{\"type\":\"hello\","
         "\"mac\":\"%s\","
         "\"name\":\"%s\","
         "\"fw\":\"%s\","
         "%s}",
        id->mac,
        id->name,
        id->fw,
        CAM_STREAM_CAPS_JSON);
    if (n < 0 || (size_t)n >= out_len) {
        return 0;
    }
    return (size_t)n;
}

size_t cam_stream_wire_build_status(const cam_stream_status_metrics_t *m,
                                     const cam_stream_identity_t *id,
                                     char *out, size_t out_len)
{
    if (m == NULL || id == NULL || out == NULL || out_len == 0) {
        return 0;
    }

    /* uptime_us → seconds. The status cadence is per-CAM_STREAM_
     * PERIOD_MS (default 30 s via W8) so sub-second precision is
     * meaningless to downstream consumers; truncate. */
    int64_t uptime_s = m->uptime_us / 1000000;

    int n = snprintf(out, out_len,
        "{\"type\":\"status\","
         "\"mac\":\"%s\","
         "\"name\":\"%s\","
         "\"uptime_s\":%lld,"
         "\"rssi_dbm\":%ld,"
         "\"free_heap\":%u,"
         "\"fb_drops\":%u,"
         "\"frames_sent\":%u,"
         "\"frames_dropped\":%u,"
         "\"fps_applied\":%u}",
        id->mac,
        id->name,
        (long long)uptime_s,
        (long)m->rssi_dbm,
        (unsigned)m->free_heap,
        (unsigned)m->fb_drops,
        (unsigned)m->frames_sent,
        (unsigned)m->frames_dropped,
        (unsigned)m->fps_applied);
    if (n < 0 || (size_t)n >= out_len) {
        return 0;
    }
    return (size_t)n;
}
