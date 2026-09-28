import { component$, useStylesScoped$ } from "@builder.io/qwik";
import styles from "./system-status-panel.css?inline";

import type { StatusState } from "~/components/status-chip/status-chip";
import { StatusChip } from "~/components/status-chip/status-chip";

/**
 * SystemStatusPanel
 *
 * A short, dense panel that sits at the top of the home page and
 * reports the live state of the stack's core services. Each row is
 * `label · value · status chip`. Values are honest: when the back
 * end is not wired, the value reads "—" and the chip reads
 * "Not connected". When the back end is wired, the value and chip
 * come from real data and the operator can trust them.
 *
 * Use as many or as few rows as needed. The panel auto-flows them
 * into a two-column grid on viewports >= md.
 */
export interface SystemStatusRow {
  label: string;
  value: string;
  status: StatusState;
  statusLabel: string;
  pulse?: boolean;
}

export interface SystemStatusPanelProps {
  rows: ReadonlyArray<SystemStatusRow>;
  /** Optional heading; defaults to "System". No eyebrow above it. */
  heading?: string;
  /** Optional last-updated timestamp string. */
  asOf?: string;
}

export const SystemStatusPanel = component$<SystemStatusPanelProps>(
  ({ rows, heading = "System", asOf }) => {
    useStylesScoped$(styles);
    return (
      <section
        class="system-status"
        aria-labelledby="system-status-heading"
      >
        <header class="system-status__head">
          <h2 id="system-status-heading" class="system-status__heading">
            {heading}
          </h2>
          {asOf ? (
            <p class="system-status__as-of" aria-label="Last updated">
              Updated <time>{asOf}</time>
            </p>
          ) : null}
        </header>

        <dl class="system-status__list">
          {rows.map((row) => (
            <div class="system-status__row" key={row.label}>
              <dt class="system-status__label">{row.label}</dt>
              <dd class="system-status__value">{row.value}</dd>
              <dd class="system-status__chip">
                <StatusChip
                  state={row.status}
                  label={row.statusLabel}
                  pulse={row.pulse}
                />
              </dd>
            </div>
          ))}
        </dl>
      </section>
    );
  },
);
