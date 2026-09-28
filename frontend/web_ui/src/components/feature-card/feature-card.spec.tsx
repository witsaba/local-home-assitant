import { renderToString } from "@builder.io/qwik/server";
import { afterAll, beforeAll, describe, expect, it, vi } from "vitest";

import { FeatureCard } from "./feature-card";

beforeAll(() => {
  vi.spyOn(console, "error").mockImplementation(() => {});
});
afterAll(() => {
  vi.restoreAllMocks();
});

async function renderCard(
  props: Partial<Parameters<typeof FeatureCard>[0]> & { title: string },
) {
  const { html } = await renderToString(
    <body>
      <div id="test-root">
        <FeatureCard
          title={props.title}
          description={props.description ?? ""}
          icon={props.icon ?? "★"}
          status={props.status ?? "not_connected"}
          statusLabel={props.statusLabel ?? "Not connected"}
          statusPulse={props.statusPulse ?? false}
          href={props.href ?? "/"}
          actionLabel={props.actionLabel ?? "Open"}
          ariaLabel={props.ariaLabel}
        />
      </div>
    </body>,
  );
  const match = html.match(/<div id="test-root">[\s\S]*?<\/div>\s*<\/body>/);
  return match ? match[0] : html;
}

describe("FeatureCard (SSR)", () => {
  it("renders the title, description, and a link to the action href", async () => {
    const fragment = await renderCard({
      title: "Devices",
      description: "Cameras and sensors on the LAN.",
      icon: "◉",
      href: "/devices",
    });
    expect(fragment).toContain("Devices");
    expect(fragment).toContain("Cameras and sensors on the LAN.");
    expect(fragment).toContain('href="/devices"');
  });

  it("renders the configured action verb (default is Open)", async () => {
    const fragment = await renderCard({
      title: "Logs",
      description: "Recent activity.",
      actionLabel: "View logs",
      href: "/logs",
    });
    expect(fragment).toContain("View logs");
  });

  it("renders the status chip with the supplied state and label", async () => {
    const fragment = await renderCard({
      title: "Discovery",
      description: "Workers LAN probes.",
      status: "healthy",
      statusLabel: "3 devices found",
    });
    expect(fragment).toContain("3 devices found");
    expect(fragment).toMatch(/class="[^"]*\bstatus-chip--healthy\b/);
  });

  it("falls back to an accessible label derived from the title", async () => {
    const fragment = await renderCard({
      title: "Settings",
      description: "Stack configuration.",
    });
    expect(fragment).toMatch(/aria-label="Settings"/);
  });

  it("honours a custom aria-label", async () => {
    const fragment = await renderCard({
      title: "Settings",
      description: "Stack configuration.",
      ariaLabel: "Settings card",
    });
    expect(fragment).toMatch(/aria-label="Settings card"/);
  });

  it("renders the icon character for the configured icon", async () => {
    const fragment = await renderCard({
      title: "Devices",
      description: "Cameras and sensors on the LAN.",
      icon: "◉",
    });
    expect(fragment).toContain("◉");
  });
});
