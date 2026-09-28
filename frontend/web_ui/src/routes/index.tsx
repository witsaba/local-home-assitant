import { component$, useStylesScoped$ } from "@builder.io/qwik";
import type { DocumentHead } from "@builder.io/qwik-city";

import { FeatureCard } from "~/components/feature-card/feature-card";
import {
  SystemStatusPanel,
  type SystemStatusRow,
} from "~/components/system-status-panel/system-status-panel";
import { TopBar } from "~/components/top-bar/top-bar";
import styles from "./index.css?inline";

/**
 * Home page (witsaba — local home assistant).
 *
 * The page is composed of:
 *   1. TopBar — brand, primary nav, system status chip
 *   2. Hero — page title + one-line orientation. No eyebrow above it.
 *   3. SystemStatusPanel — live state of postgres / messaging-core /
 *      workers / devices
 *   4. FeatureCard grid — Devices, Discovery, Logs, Settings, each
 *      with its own real status
 *   5. Footer — version + build commit
 *
 * THESIS (modernization): the page is a status board, not a story.
 * OWN-WORLD: warm-neutral surfaces, signal-blue accent, system
 * typography, generous spacing, real status chips. STORY: a calm
 * "operate" surface for a self-hosting operator. FIRST VIEWPORT:
 * top bar visible, hero "Stack overview" + the system status panel
 * both above the fold on a 1024x768 desktop. FORM: Calm Operational
 * (Operate mode), seed key witsaba/calm-operational. FINISH:
 * unreviewed and undocumented is unfinished; this build ends with
 * the finish review, the verdict, and DESIGN.md.
 */
export default component$(() => {
  useStylesScoped$(styles);

  // Live system rows. In v1 the back end is not yet wired into the
  // page, so every value reads "—" and every status is
  // "not_connected". When the API lands, replace these with the
  // real query results — the layout and tokens stay.
  const systemRows: SystemStatusRow[] = [
    {
      label: "Postgres",
      value: "—",
      status: "not_connected",
      statusLabel: "Not connected",
    },
    {
      label: "messaging-core",
      value: "—",
      status: "not_connected",
      statusLabel: "Not connected",
    },
    {
      label: "workers",
      value: "—",
      status: "not_connected",
      statusLabel: "Not connected",
    },
    {
      label: "Discovered devices",
      value: "0",
      status: "not_connected",
      statusLabel: "No data yet",
    },
  ];

  return (
    <>
      <TopBar
        currentPath="/"
        systemStatus="not_connected"
        systemStatusLabel="Stack offline"
      />

      <main class="home" id="main">
        <header class="home__hero">
          <h1 class="home__title">Stack overview</h1>
          <p class="home__lede">
            Live status of the witsaba local-home-assistant stack. The
            cards below are placeholders for the feature surfaces —
            each one will host a dedicated view once the corresponding
            back-end wiring lands.
          </p>
        </header>

        <SystemStatusPanel
          heading="System"
          asOf="not yet connected"
          rows={systemRows}
        />

        <section class="home__grid" aria-label="Feature surfaces">
          <FeatureCard
            title="Devices"
            description="Cameras, sensors, and actuators discovered on the LAN."
            icon="◉"
            status="not_connected"
            statusLabel="Not connected"
            href="/devices"
            actionLabel="Open"
          />
          <FeatureCard
            title="Discovery"
            description="Live status of the workers LAN discovery probes."
            icon="⌕"
            status="not_connected"
            statusLabel="Not connected"
            href="/discovery"
            actionLabel="Open"
          />
          <FeatureCard
            title="Logs"
            description="Recent activity from workers, messaging-core, and the UI."
            icon="≡"
            status="not_connected"
            statusLabel="Not connected"
            href="/logs"
            actionLabel="Open"
          />
          <FeatureCard
            title="Settings"
            description="Stack configuration, secrets, and adapter selection."
            icon="⚙"
            status="not_connected"
            statusLabel="Not connected"
            href="/settings"
            actionLabel="Open"
          />
        </section>
      </main>

      <footer class="home__footer" role="contentinfo">
        <div class="home__footer-inner">
          <span class="home__footer-text">
            witsaba local-home-assistant
          </span>
          <span class="home__footer-meta">
            v0.1.0-dev · <code>unset</code>
          </span>
        </div>
      </footer>
    </>
  );
});

export const head: DocumentHead = {
  title: "witsaba — Local home assistant",
  meta: [
    {
      name: "description",
      content:
        "Live status of the witsaba local-home-assistant stack — Postgres, messaging-core, workers, and discovered devices.",
    },
    {
      name: "color-scheme",
      content: "light dark",
    },
    {
      name: "theme-color",
      content: "#f7f7f5",
      media: "(prefers-color-scheme: light)",
    },
    {
      name: "theme-color",
      content: "#0e1014",
      media: "(prefers-color-scheme: dark)",
    },
  ],
};
