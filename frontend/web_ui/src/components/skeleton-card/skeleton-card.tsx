import { component$, Slot, useStylesScoped$ } from "@builder.io/qwik";
import styles from "./skeleton-card.css?inline";

/**
 * SkeletonCard
 *
 * Placeholder card used in the home page wireframe. Represents one
 * feature surface in the witsaba local-home-assistant UI (e.g.
 * "Devices", "Discovery", "Logs", "Settings"). Title and body text
 * are static placeholders — replace per-card when a real feature is
 * scaffolded.
 *
 * The component is intentionally minimal so it stays trivially
 * testable. Future feature components will own their own state and
 * route-level integration.
 */
export interface SkeletonCardProps {
  /** Headline shown at the top of the card. */
  title: string;
  /** One-line description shown under the title. */
  description: string;
  /** Optional label for a future action button. */
  actionLabel?: string;
}

export const SkeletonCard = component$<SkeletonCardProps>(
  ({ title, description, actionLabel }) => {
    useStylesScoped$(styles);

    return (
      <article class="skeleton-card" data-testid="skeleton-card">
        <h2 class="skeleton-card__title">{title}</h2>
        <p class="skeleton-card__description">{description}</p>
        <Slot />
        {actionLabel ? (
          <button type="button" class="skeleton-card__action" disabled>
            {actionLabel}
          </button>
        ) : null}
      </article>
    );
  },
);
