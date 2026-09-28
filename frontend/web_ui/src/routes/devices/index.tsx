import { component$, useStylesScoped$ } from "@builder.io/qwik";
import { routeLoader$, type DocumentHead } from "@builder.io/qwik-city";
import styles from "./index.css?inline";

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

export interface ApiDevice {
  mac: string;
  name: string;
  fw: string;
  chip: string;
  last_source_ip: string;
  last_seen_at: string;
}

// ---------------------------------------------------------------------------
// Data loader (server-side)
// ---------------------------------------------------------------------------

/**
 * Fetches active devices from the messaging-core REST API.
 * Returns only devices whose last_seen_at is within the last 60 seconds.
 * Fails gracefully: the page renders with an error state instead of crashing.
 */
export const useActiveDevices = routeLoader$(async () => {
  // Allow the API base URL to be overridden via env var.
  const base = (import.meta.env["VITE_PUBLIC_API_BASE_URL"] as string | undefined) ??
    "http://127.0.0.1:8081";

  try {
    const res = await fetch(`${base}/api/devices/active`, {
      signal: AbortSignal.timeout(5000),
    });
    if (!res.ok) {
      throw new Error(`API returned ${res.status}`);
    }
    const data: ApiDevice[] = await res.json();
    return { devices: data, ok: true as const };
  } catch (e: unknown) {
    const msg = e instanceof Error ? e.message : String(e);
    return { devices: [], ok: false as const, reason: msg };
  }
});

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

/** Formats a RFC3339 timestamp as a human-readable relative string. */
function relativeTime(iso: string): string {
  try {
    const diff = Date.now() - new Date(iso).getTime();
    const sec = Math.floor(diff / 1000);
    if (sec < 10) return "just now";
    if (sec < 60) return `${sec}s ago`;
    const min = Math.floor(sec / 60);
    if (min < 60) return `${min}m ago`;
    const hr = Math.floor(min / 60);
    return `${hr}h ago`;
  } catch {
    return iso;
  }
}

/** Returns the display name for a device, falling back to the truncated MAC. */
function deviceLabel(device: ApiDevice): string {
  if (device.name) return device.name;
  // Truncate MAC to the last 6 hex chars for readability
  const mac = device.mac.replace(/[:-]/g, "");
  return `cam-${mac.slice(-6).toUpperCase()}`;
}

// ---------------------------------------------------------------------------
// Component
// ---------------------------------------------------------------------------

export default component$(() => {
  useStylesScoped$(styles);

  const devices = useActiveDevices();

  return (
    <div class="devices-page">
      <header class="devices-page__header">
        <h1 class="devices-page__title">Active devices</h1>
        <p class="devices-page__subtitle">
          Cameras and sensors seen in the last 60 seconds.
        </p>
      </header>

      {/* API unreachable — show error banner but still render the page shell */}
      {!devices.value.ok && (
        <div class="devices-page__alert" role="alert">
          <span class="devices-page__alert-icon">⚠</span>
          <span>
            Could not reach the devices API:{" "}
            <code>{devices.value.reason}</code>. Showing cached or empty state.
          </span>
        </div>
      )}

      {/* Device grid */}
      {devices.value.devices.length === 0 && devices.value.ok ? (
        <div class="devices-page__empty">
          <p class="devices-page__empty-icon">◌</p>
          <p class="devices-page__empty-title">No active devices</p>
          <p class="devices-page__empty-body">
            Workers will discover devices when they broadcast on the LAN.
            The list refreshes automatically.
          </p>
        </div>
      ) : (
        <ul class="devices-page__grid" aria-label="Active devices">
          {devices.value.devices.map((device) => (
            <li key={device.mac} class="device-card">
              <div class="device-card__head">
                <span class="device-card__name">{deviceLabel(device)}</span>
                <span
                  class="device-card__status-dot"
                  aria-label="Online"
                  title="Online — seen within 60 seconds"
                />
              </div>

              <dl class="device-card__fields">
                <div class="device-card__field">
                  <dt>MAC</dt>
                  <dd>
                    <code class="device-card__mono">{device.mac}</code>
                  </dd>
                </div>
                {device.last_source_ip && (
                  <div class="device-card__field">
                    <dt>IP</dt>
                    <dd>
                      <code class="device-card__mono">{device.last_source_ip}</code>
                    </dd>
                  </div>
                )}
                {device.fw && (
                  <div class="device-card__field">
                    <dt>FW</dt>
                    <dd>
                      <code class="device-card__mono">{device.fw}</code>
                    </dd>
                  </div>
                )}
                {device.chip && (
                  <div class="device-card__field">
                    <dt>Chip</dt>
                    <dd>
                      <code class="device-card__mono">{device.chip}</code>
                    </dd>
                  </div>
                )}
                <div class="device-card__field">
                  <dt>Last seen</dt>
                  <dd>
                    <time dateTime={device.last_seen_at}>
                      {relativeTime(device.last_seen_at)}
                    </time>
                  </dd>
                </div>
              </dl>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
});

// ---------------------------------------------------------------------------
// Document head
// ---------------------------------------------------------------------------

export const head: DocumentHead = {
  title: "Devices — witsaba",
  meta: [
    {
      name: "description",
      content: "Active cameras and sensors on the witsaba LAN.",
    },
  ],
};
