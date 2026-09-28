import { renderToString } from "@builder.io/qwik/server";
import { afterAll, beforeAll, describe, expect, it, vi } from "vitest";

import { StatusChip } from "./status-chip";

beforeAll(() => {
  vi.spyOn(console, "error").mockImplementation(() => {});
});
afterAll(() => {
  vi.restoreAllMocks();
});

async function renderChip(props: Parameters<typeof StatusChip>[0]) {
  const { html } = await renderToString(
    <body>
      <div id="test-root">
        <StatusChip {...props} />
      </div>
    </body>,
  );
  const match = html.match(/<div id="test-root">[\s\S]*?<\/div>\s*<\/body>/);
  return match ? match[0] : html;
}

describe("StatusChip (SSR)", () => {
  it("renders the label and an icon for the healthy state", async () => {
    const fragment = await renderChip({ state: "healthy", label: "Connected" });
    expect(fragment).toContain("Connected");
    expect(fragment).toContain('data-state="healthy"');
    expect(fragment).toMatch(/class="[^"]*\bstatus-chip--healthy\b/);
  });

  it("renders the unhealthy state with the error palette class", async () => {
    const fragment = await renderChip({ state: "unhealthy", label: "Down" });
    expect(fragment).toContain("Down");
    expect(fragment).toContain('data-state="unhealthy"');
    expect(fragment).toMatch(/class="[^"]*\bstatus-chip--unhealthy\b/);
  });

  it("renders the not_connected state as a dashed, muted pill", async () => {
    const fragment = await renderChip({
      state: "not_connected",
      label: "Not connected",
    });
    expect(fragment).toContain("Not connected");
    expect(fragment).toMatch(/class="[^"]*\bstatus-chip--not_connected\b/);
  });

  it("exposes a screen-reader label that names the state", async () => {
    const fragment = await renderChip({ state: "degraded", label: "Slow" });
    expect(fragment).toMatch(/aria-label="Status: Slow"/);
  });

  it("renders the pulse class when the pulse flag is true", async () => {
    const fragment = await renderChip({
      state: "healthy",
      label: "Recovered",
      pulse: true,
    });
    expect(fragment).toMatch(/class="[^"]*\bstatus-chip--pulse\b/);
  });

  it("does not render the pulse class by default", async () => {
    const fragment = await renderChip({ state: "healthy", label: "Steady" });
    expect(fragment).not.toMatch(/\bstatus-chip--pulse\b/);
  });
});
