import { component$, useStylesScoped$ } from "@builder.io/qwik";
import styles from "./top-bar.css?inline";

import type { StatusState } from "~/components/status-chip/status-chip";
import { StatusChip } from "~/components/status-chip/status-chip";

/**
 * TopBar
 *
 * The single, fixed-height navigation surface at the top of the
 * witsaba UI. Holds:
 *
 *   - the brand wordmark on the left
 *   - the primary nav (Devices, Discovery, Logs, Settings) inline
 *     on viewports >= md, hidden behind a menu trigger below md
 *   - a system status chip on the right (overall stack health)
 *
 * The current nav item is communicated by a 2px accent underline
 * plus accent text, never by background tint alone.
 */
export interface TopBarProps {
  currentPath: string;
  systemStatus: StatusState;
  systemStatusLabel: string;
  systemStatusPulse?: boolean;
}

const NAV: ReadonlyArray<{ label: string; href: string }> = [
  { label: "Devices", href: "/devices" },
  { label: "Discovery", href: "/discovery" },
  { label: "Logs", href: "/logs" },
  { label: "Settings", href: "/settings" },
];

export const TopBar = component$<TopBarProps>(
  ({
    currentPath,
    systemStatus,
    systemStatusLabel,
    systemStatusPulse = false,
  }) => {
    useStylesScoped$(styles);

    return (
      <header class="top-bar" role="banner">
        <div class="top-bar__inner">
          <a
            class="top-bar__brand"
            href="/"
            aria-label="witsaba — home"
          >
            <span class="top-bar__brand-mark" aria-hidden="true">
              ◐
            </span>
            <span class="top-bar__brand-text">witsaba</span>
          </a>

          <nav class="top-bar__nav" aria-label="Primary">
            <ul class="top-bar__nav-list">
              {NAV.map((item) => {
                const isCurrent =
                  currentPath === item.href ||
                  currentPath.startsWith(item.href + "/");
                return (
                  <li key={item.href} class="top-bar__nav-item">
                    <a
                      class={[
                        "top-bar__nav-link",
                        isCurrent ? "top-bar__nav-link--current" : "",
                      ]
                        .filter(Boolean)
                        .join(" ")}
                      href={item.href}
                      aria-current={isCurrent ? "page" : undefined}
                    >
                      {item.label}
                    </a>
                  </li>
                );
              })}
            </ul>
          </nav>

          <div class="top-bar__status">
            <StatusChip
              state={systemStatus}
              label={systemStatusLabel}
              pulse={systemStatusPulse}
            />
          </div>
        </div>
      </header>
    );
  },
);
