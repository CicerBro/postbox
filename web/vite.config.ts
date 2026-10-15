import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";
import { readFileSync } from "node:fs";

const themeScript = '<script src="/src/theme-init.js"></script>';
const themePlaceholder = "<!-- postbox-theme-init -->";

export default defineConfig({
  plugins: [react(), tailwindcss(), {
    name: "postbox-theme-init",
    // Keep the bootstrap synchronous: module scripts run too late for first paint.
    transformIndexHtml: {
      order: "pre",
      handler: (html, context) => context.server ? html : html.replace(themeScript, themePlaceholder),
    },
    generateBundle: {
      order: "post",
      handler(_, bundle) {
        const html = bundle["index.html"];
        if (html?.type !== "asset") throw new Error("Missing app HTML");
        const sourcePath = new URL("./src/theme-init.js", import.meta.url);
        this.addWatchFile(sourcePath.pathname);
        const script = this.emitFile({ type: "asset", name: "theme-init.js", source: readFileSync(sourcePath) });
        html.source = String(html.source).replace(themePlaceholder, `<script src="/${this.getFileName(script)}"></script>`);
      },
    },
  }],
  build: { outDir: "../internal/web/dist", emptyOutDir: true },
  server: { proxy: { "/api": "http://localhost:8080" } },
});
