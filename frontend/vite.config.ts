import { defineConfig } from "vite";
import { svelte } from "@sveltejs/vite-plugin-svelte";
import tailwindcss from "@tailwindcss/vite";
import wails from "@wailsio/runtime/plugins/vite";

// Manual chunk splits for CodeMirror and xterm so the main bundle stays
// small and these heavy editors get their own cacheable chunks. Browsers
// fetch them in parallel, and once cached they survive across refreshes
// even when the app code changes.
export default defineConfig({
  plugins: [tailwindcss(), svelte(), wails("./bindings")],
  build: {
    rollupOptions: {
      output: {
        manualChunks(id: string) {
          if (id.includes("node_modules")) {
            if (id.includes("codemirror") || id.includes("@codemirror")) {
              return "codemirror";
            }
            if (id.includes("xterm") || id.includes("@xterm")) {
              return "xterm";
            }
            if (id.includes("@lucide/svelte")) {
              return "icons";
            }
          }
        },
      },
    },
    chunkSizeWarningLimit: 800,
  },
});
