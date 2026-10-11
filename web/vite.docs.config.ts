import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import { resolve } from "node:path";
import { readFileSync } from "node:fs";
import { buildGuides, guideSources, legacyRoutes } from "./docs/content";

const repository = resolve(import.meta.dirname, "..");
export default defineConfig({
  root: resolve(import.meta.dirname, "docs"),
  base: "/docs/",
  publicDir: false,
  cacheDir: resolve(import.meta.dirname, "node_modules/.vite-docs"),
  plugins: [react(), {
    name: "postbox-guides",
    resolveId(id) { if (id === "virtual:postbox-guides") return "\0postbox-guides"; },
    load(id) {
      if (id !== "\0postbox-guides") return;
      for (const file of guideSources) this.addWatchFile(resolve(repository, file));
      return `export const legacyRoutes = ${JSON.stringify(legacyRoutes)}; export default ${JSON.stringify(buildGuides(repository))}`;
    },
    generateBundle() {
      this.emitFile({ type: "asset", fileName: "assets/postbox-campaigns.png", source: readFileSync(resolve(repository, "docs/images/postbox-campaigns.png")) });
    },
    configureServer(server) {
      server.middlewares.use((request, response, next) => {
        if (request.url?.split("?")[0] !== "/docs/assets/postbox-campaigns.png") return next();
        response.setHeader("Content-Type", "image/png");
        response.end(readFileSync(resolve(repository, "docs/images/postbox-campaigns.png")));
      });
    },
  }],
  build: { outDir: resolve(repository, "internal/web/dist/docs"), emptyOutDir: true },
  server: { proxy: { "/api": "http://localhost:8080", "/docs/openapi.json": "http://localhost:8080" }, fs: { allow: [repository] } },
});
