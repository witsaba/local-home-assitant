/**
 * Vitest configuration for the witsaba-web-ui project.
 *
 * Kept in a separate file from vite.config.ts on purpose: the Qwik docs
 * recommend a dedicated config so the Vitest transform pipeline does
 * not fight with the QwikCity SSR plugin that vite.config.ts wires up.
 * Tests run the Qwik optimizer through the qwikVite plugin below, which
 * is enough to compile component$ and the `$` boundary markers.
 *
 * Reference: https://qwik.dev/docs/integrations/vitest/
 */
import { defineConfig } from "vitest/config";
import { qwikVite } from "@builder.io/qwik/optimizer";
import tsconfigPaths from "vite-tsconfig-paths";

export default defineConfig({
  plugins: [qwikVite(), tsconfigPaths({ root: "." })],
  test: {
    // jsdom is the de facto DOM environment for Qwik component tests.
    // Real-browser mode is a deliberate future upgrade (see
    // vitest-browser-qwik in odd/tasks/qwik-web-ui-scaffold.md).
    environment: "jsdom",
    // Tests live next to source files as *.spec.ts(x) or *.test.ts(x).
    include: ["src/**/*.{spec,test}.{ts,tsx}"],
    // Qwik test files import @builder.io/qwik/testing which is ESM.
    globals: false,
    css: false,
  },
  resolve: {
    // Mirror the alias set declared in tsconfig.json so test files
    // resolve `~/...` the same way the app does.
    alias: {
      "~": new URL("./src", import.meta.url).pathname,
    },
  },
});
