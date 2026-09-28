import { renderToString } from "@builder.io/qwik/server";
import { afterAll, beforeAll, describe, expect, it, vi } from "vitest";

import { SkeletonCard } from "./skeleton-card";

/**
 * Tests for the SkeletonCard component.
 *
 * These tests use Qwik's server-side `renderToString` rather than the
 * DOM-based `createDOM` helper. As of @builder.io/qwik 1.19+, the
 * bundled DOM testing layer has a known regression where the wrapped
 * HTMLUnknownElement2 calls `isAncestor` on a raw DOM child node that
 * does not have the polyfill installed (see
 * odd/tasks/qwik-web-ui-scaffold.md for the full analysis). The
 * `renderToString` path is unaffected because it never touches the
 * browser DOM — it serialises to a string and we assert on the HTML.
 *
 * renderToString expects a valid HTML document root. A bare <article>
 * is not a valid child of <html>, so each test wraps the component
 * inside <body> > <div>. Qwik complains at the console that no
 * <head> is present — that error is silenced below because the
 * scoped class prefix is still applied to the markup and the
 * assertions still hold.
 *
 * Qwik's useStylesScoped$ rewrites class names with a per-component
 * scope (e.g. "skeleton-card" -> "⭐3p3pb1-0 skeleton-card"). We
 * assert with regex that ends in the original class name so the test
 * stays stable across Qwik scope-id changes.
 */
async function renderCard(props: {
  title: string;
  description: string;
  actionLabel?: string;
}): Promise<string> {
  const { html } = await renderToString(
    <body>
      <div id="test-root">
        <SkeletonCard {...props} />
      </div>
    </body>,
  );
  const match = html.match(/<div id="test-root">[\s\S]*?<\/div>\s*<\/body>/);
  return match ? match[0] : html;
}

describe("SkeletonCard (SSR)", () => {
  // Qwik logs a non-fatal error when useStylesScoped$ cannot find a
  // <head> to inject the scoped styles into. The component still
  // renders correctly, so the assertions pass — but the noise makes
  // the output unreadable in CI. Silence the error channel for the
  // duration of this suite.
  beforeAll(() => {
    vi.spyOn(console, "error").mockImplementation(() => {});
  });
  afterAll(() => {
    vi.restoreAllMocks();
  });

  it("renders the title and description as visible text", async () => {
    const fragment = await renderCard({
      title: "Devices",
      description: "Discovered cameras and sensors on the LAN.",
    });

    expect(fragment).toContain("Devices");
    expect(fragment).toContain("Discovered cameras and sensors on the LAN.");
    expect(fragment).toMatch(/class="[^"]*\bskeleton-card\b/);
    expect(fragment).toMatch(/class="[^"]*\bskeleton-card__title\b/);
    expect(fragment).toMatch(/class="[^"]*\bskeleton-card__description\b/);
  });

  it("renders the action button when actionLabel is provided", async () => {
    const fragment = await renderCard({
      title: "Logs",
      description: "Recent worker activity.",
      actionLabel: "Open",
    });

    expect(fragment).toMatch(
      /<button[^>]*\bclass="[^"]*\bskeleton-card__action\b/,
    );
    expect(fragment).toContain("Open");
    // The button is a visual placeholder and must not be interactive.
    expect(fragment).toMatch(/<button[^>]*\sdisabled/);
  });

  it("does not render the action button when actionLabel is omitted", async () => {
    const fragment = await renderCard({
      title: "Settings",
      description: "Stack configuration.",
    });

    expect(fragment).not.toMatch(/\bskeleton-card__action\b/);
    expect(fragment).toContain("Settings");
    expect(fragment).toContain("Stack configuration.");
  });
});
