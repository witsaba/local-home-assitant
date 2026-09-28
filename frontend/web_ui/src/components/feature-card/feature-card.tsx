import { component$, Slot, useStylesScoped$ } from "@builder.io/qwik";
import styles from "./feature-card.css?inline";

import type { StatusState } from "~/components/status-chip/status-chip";
import { StatusChip } from "~/components/status-chip/status-chip";

/**
 * FeatureCard
 *
 * A surface-level card used on the home grid (and reusable for any
 * "feature overview" surface in the stack). Each card represents one
 * functional area — Devices, Discovery, Logs, Settings — and carries:
 *
 *   - a small icon in the top-left
 *   - a title and a one-line description
 *   - a real status chip in the top-right (never a green check on a
 *     feature that has no live data behind it; the operator would
 *     notice and stop trusting the surface)
 *   - a single primary "Open" link (or another action verb) that is
 *     the card's only chrome — the whole card is the click target
 *
 * The card intentionally has no internal button. The `<a>` is the
 * only interactive element so the operator's click target is
 * unambiguous and the keyboard focus ring is one shape.
 */
export interface FeatureCardProps {
  /** Accessible label for the card. Falls back to the title. */
  ariaLabel?: string;
  /** Headline shown at the top of the card. */
  title: string;
  /** One-line description shown under the title. */
  description: string;
  /** Inline icon. Should be a 20x20 SVG path or single-glyph character. */
  icon: string;
  /** Status of the underlying feature. */
  status: StatusState;
  /** Label rendered inside the status chip. */
  statusLabel: string;
  /** Optional pulse flag for the one-time state-change moment. */
  statusPulse?: boolean;
  /** Href for the card's primary action. The whole card links here. */
  href: string;
  /** Verb for the primary link, default "Open". */
  actionLabel?: string;
}

export const FeatureCard = component$<FeatureCardProps>(
  ({
    ariaLabel,
    title,
    description,
    icon,
    status,
    statusLabel,
    statusPulse = false,
    href,
    actionLabel = "Open",
  }) => {
    useStylesScoped$(styles);
    const label = ariaLabel ?? title;

    return (
      <article class="feature-card" aria-label={label}>
        <header class="feature-card__head">
          <span class="feature-card__icon" aria-hidden="true">
            {icon}
          </span>
          <StatusChip
            state={status}
            label={statusLabel}
            pulse={statusPulse}
          />
        </header>

        <h2 class="feature-card__title">{title}</h2>
        <p class="feature-card__description">{description}</p>

        <Slot />

        <a class="feature-card__action" href={href}>
          <span>{actionLabel}</span>
          <span class="feature-card__arrow" aria-hidden="true">
            →
          </span>
        </a>
      </article>
    );
  },
);
