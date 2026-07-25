import { fileURLToPath } from "node:url";

import { defineConfig } from "vite";

// Only development mode gets the browser mock; every other mode (including the
// production bundle Wails embeds) resolves the real generated bindings.
export default defineConfig(({ mode }) => ({
  resolve: {
    alias: {
      "gorilla-api-impl": fileURLToPath(
        new URL(mode === "development" ? "./src/mock-api.ts" : "./src/wails-api.ts", import.meta.url),
      ),
    },
  },
}));
