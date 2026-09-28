import { component$, useStylesScoped$ } from "@builder.io/qwik";
import styles from "./status-chip.css?inline";

/**
 * StatusChip
 *
 * A small pill that combines a leading icon (a dot or geometric
 * glyph) with a short label, used to communicate the health of a
 * system component. State is communicated by icon + label *and*
 * color — never color alone, so the chip remains legible in dark
 * mode and to color-blind users.
 *
 * States (kept to a small, honest set):
 *   - healthy:    solid dot, accent-tone label
 *   - degraded:   half dot, warning-tone label
 *   - unhealthy:  cross-hair glyph, error-tone label
 *   - connecting: dashed dot, muted label
 *   - not_connected: dashed circle, muted label
 *
 * The chip does not own motion. It exposes a `pulse` flag the
 * caller sets only on a real state change, which triggers the one
 * authored moment in this surface.
 */
export type StatusState =
  | "healthy"
  | "degraded"
  | "unhealthy"
  | "connecting"
  | "not_connected";

export interface StatusChipProps {
  state: StatusState;
  label: string;
  /** When true, plays the one-time state-change pulse. */
  pulse?: boolean;
}

const ICON: Record<StatusState, string> = {
  healthy: "●",
  degraded: "◐",
  unhealthy: "✕",
  connecting: "◌",
  not_connected: "○",
};

export const StatusChip = component$<StatusChipProps>(
  ({ state, label, pulse = false }) => {
    useStylesScoped$(styles);
    const icon = ICON[state];
    const className = [
      "status-chip",
      `status-chip--${state}`,
      pulse ? "status-chip--pulse" : "",
    ]
      .filter(Boolean)
      .join(" ");

    return (
      <span
        class={className}
        role="status"
        aria-label={`Status: ${label}`}
        data-state={state}
      >
        <span class="status-chip__icon" aria-hidden="true">
          {icon}
        </span>
        <span class="status-chip__label">{label}</span>
      </span>
    );
  },
);
