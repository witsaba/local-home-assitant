import { component$, useStylesScoped$ } from "@builder.io/qwik";
import type { DocumentHead } from "@builder.io/qwik-city";

import { SkeletonCard } from "~/components/skeleton-card/skeleton-card";
import styles from "./index.css?inline";

/**
 * Home page (skeleton).
 *
 * Visual wireframe for the witsaba local-home-assistant UI. Each
 * SkeletonCard represents a future feature surface. Replace
 * placeholder copy and the card body per feature when those features
 * land — keep this file as the only top-level route until then.
 */
export default component$(() => {
  useStylesScoped$(styles);

  return (
    <div class="home">
      <nav class="home__nav" aria-label="Primary">
        <span class="home__brand">witsaba</span>
        <div class="home__nav-links" aria-hidden="true">
          <span>Devices</span>
          <span>Discovery</span>
          <span>Logs</span>
          <span>Settings</span>
        </div>
      </nav>

      <main class="home__main">
        <section class="home__hero">
          <span class="home__hero-eyebrow">Local home assistant</span>
          <h1 class="home__hero-title">Stack overview</h1>
          <p class="home__hero-subtitle">
            Front-end wireframe for the witsaba stack. The cards below are
            placeholders — each one will host a dedicated feature once the
            corresponding back-end service is wired in.
          </p>
        </section>

        <section class="home__grid" aria-label="Feature surfaces">
          <SkeletonCard
            title="Devices"
            description="Cameras, sensors, and actuators discovered on the LAN."
            actionLabel="Open"
          />
          <SkeletonCard
            title="Discovery"
            description="Live status of the workers discovery probes."
            actionLabel="Open"
          />
          <SkeletonCard
            title="Logs"
            description="Recent activity from workers, messaging-core, and the UI."
            actionLabel="Open"
          />
          <SkeletonCard
            title="Settings"
            description="Stack configuration, secrets, and adapter selection."
            actionLabel="Open"
          />
        </section>
      </main>

      <footer class="home__footer">
        witsaba local-home-assistant — UI scaffold
      </footer>
    </div>
  );
});

export const head: DocumentHead = {
  title: "witsaba — Local home assistant",
  meta: [
    {
      name: "description",
      content:
        "Front-end for the witsaba local-home-assistant stack. Wireframe skeleton.",
    },
  ],
};
