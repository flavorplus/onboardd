import { defineConfig } from "vite";

import { mockSetupAPI } from "./dev/mock-api.ts";

export default defineConfig(({ mode }) => {
  const policy = mode === "network-only" ? "network" : mode === "standalone-only" ? "standalone" : "both";
  return {
    plugins: [mockSetupAPI(policy)],
    build: {
      outDir: "dist",
      emptyOutDir: true,
      target: "es2020",
    },
    server: {
      port: 5173,
      strictPort: true,
    },
  };
});
