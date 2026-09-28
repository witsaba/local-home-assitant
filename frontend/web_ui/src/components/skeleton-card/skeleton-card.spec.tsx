import { createDOM } from "@builder.io/qwik/testing";
import { describe, expect, it } from "vitest";

import { SkeletonCard } from "./skeleton-card";

describe("SkeletonCard", () => {
  it("renders the title and description as plain text", async () => {
    const { screen, render } = await createDOM();
    await render(
      <SkeletonCard
        title="Devices"
        description="Discovered cameras and sensors on the LAN."
      />,
    );

    const card = screen.querySelector('[data-testid="skeleton-card"]');
    expect(card).toBeTruthy();
    expect(card?.outerHTML).toContain("Devices");
    expect(card?.outerHTML).toContain(
      "Discovered cameras and sensors on the LAN.",
    );
  });

  it("renders the action button when actionLabel is provided", async () => {
    const { screen, render } = await createDOM();
    await render(
      <SkeletonCard
        title="Logs"
        description="Recent worker activity."
        actionLabel="Open"
      />,
    );

    const button = screen.querySelector("button.skeleton-card__action");
    expect(button).toBeTruthy();
    expect(button?.outerHTML).toContain("Open");
    // The button is a visual placeholder and must not be interactive yet.
    expect(button?.hasAttribute("disabled")).toBe(true);
  });

  it("does not render the action button when actionLabel is omitted", async () => {
    const { screen, render } = await createDOM();
    await render(
      <SkeletonCard title="Settings" description="Stack configuration." />,
    );

    expect(screen.querySelector("button.skeleton-card__action")).toBeNull();
  });
});
