import { renderToString } from "@builder.io/qwik/server";
import { afterAll, beforeAll, describe, expect, it, vi } from "vitest";

import { SystemStatusPanel } from "./system-status-panel";

beforeAll(() => {
  vi.spyOn(console, "error").mockImplementation(() => {});
});
afterAll(() => {
  vi.restoreAllMocks();
});

async function renderPanel(
  props: Parameters<typeof SystemStatusPanel>[0],
) {
  const { html } = await renderToString(
    <body>
      <div id="test-root">
        <SystemStatusPanel {...props} />
      </div>
    </body>,
  );
  const match = html.match(/<div id="test-root">[\s\S]*?<\/div>\s*<\/body>/);
  return match ? match[0] : html;
}

describe("SystemStatusPanel (SSR)", () => {
  it("renders the heading and every row's label and value", async () => {
    const fragment = await renderPanel({
      heading: "System",
      asOf: "12:34:56",
      rows: [
        {
          label: "Postgres",
          value: "127.0.0.1:5432",
          status: "healthy",
          statusLabel: "Connected",
        },
        {
          label: "Workers",
          value: "1 instance",
          status: "degraded",
          statusLabel: "Slow",
        },
      ],
    });

    expect(fragment).toContain("System");
    expect(fragment).toContain("Postgres");
    expect(fragment).toContain("127.0.0.1:5432");
    expect(fragment).toContain("Workers");
    expect(fragment).toContain("1 instance");
    expect(fragment).toContain("12:34:56");
  });

  it("renders the healthy status chip class on a healthy row", async () => {
    const fragment = await renderPanel({
      rows: [
        {
          label: "Postgres",
          value: "5432",
          status: "healthy",
          statusLabel: "Up",
        },
      ],
    });
    expect(fragment).toMatch(/class="[^"]*\bstatus-chip--healthy\b/);
  });

  it("renders the degraded status chip class on a degraded row", async () => {
    const fragment = await renderPanel({
      rows: [
        {
          label: "Workers",
          value: "1",
          status: "degraded",
          statusLabel: "Slow",
        },
      ],
    });
    expect(fragment).toMatch(/class="[^"]*\bstatus-chip--degraded\b/);
  });

  it("does not render the as-of line when the asOf prop is omitted", async () => {
    const fragment = await renderPanel({
      rows: [
        {
          label: "Postgres",
          value: "5432",
          status: "not_connected",
          statusLabel: "Off",
        },
      ],
    });
    expect(fragment).not.toContain("Updated");
  });

  it("uses a <dl> as the semantic list root for assistive tech", async () => {
    const fragment = await renderPanel({
      rows: [
        {
          label: "Postgres",
          value: "5432",
          status: "not_connected",
          statusLabel: "Off",
        },
      ],
    });
    expect(fragment).toMatch(/<dl[^>]*\bsystem-status__list\b/);
  });
});
