import { defineConfig } from "vite";

import { mockSetupAPI } from "./dev/mock-api.ts";

export default defineConfig(({ mode }) => {
  const policy =
    mode === "network-only" ? "network" : mode === "standalone-only" ? "standalone" : "both";
  const brand = mode === "branded" ? "custom" : "default";
  return {
    plugins: [mockSetupAPI(policy, brand)],
    build: {
      // The production binary embeds this directory. Keeping the build output beside
      // the Go wrapper makes a fresh checkout buildable without a runtime asset copy.
      outDir: "../internal/webui/dist",
      emptyOutDir: true,
      target: "es2020",
      rollupOptions: {
        input: {
          setup: "index.html",
          landing: "landing.html",
        },
      },
    },
    server: {
      port: 5173,
      strictPort: true,
    },
  };
});
