/**
 * Vitest configuration for the witsaba-web-ui project.
 *
 * Kept in a separate file from vite.config.ts on purpose: the Qwik docs
 * recommend a dedicated config so the Vitest transform pipeline does
 * not fight with the QwikCity SSR plugin that vite.config.ts wires up.
 * Reference: https://qwik.dev/docs/integrations/vitest/
 *
 * The component specs use Qwik's server-side `renderToString` and
 * never touch the live DOM, so the test environment choice is mostly
 * defensive (jsdom is required by some transitive imports). We pick
 * jsdom — the documented default — for the closest possible match
 * to the official Qwik docs example.
 *
 * `vite-tsconfig-paths` makes the `~/*` alias declared in
 * `tsconfig.json` resolve under the test runner, the same way it
 * resolves in the dev and build pipelines. Without it, the
 * optimizer step that splits each component$ into its own module
 * loses the alias and tests fail at import-analysis time.
 */
import { defineConfig } from "vitest/config";
import { qwikVite } from "@builder.io/qwik/optimizer";
import tsconfigPaths from "vite-tsconfig-paths";

export default defineConfig({
  plugins: [qwikVite(), tsconfigPaths({ root: "." })],
  test: {
    environment: "jsdom",
    include: ["src/**/*.{spec,test}.{ts,tsx}"],
    globals: false,
    css: false,
  },
});
