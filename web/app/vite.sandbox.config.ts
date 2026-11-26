import { fileURLToPath } from "node:url";
import { defineConfig } from "vite";

// The artifact sandbox's renderers (P9-03, decision 0421), built after the
// main page into web/dist/sandbox/, one classic script each with nothing
// split out: the sandboxed frame is a page of no origin, which can run a
// classic script from Keystone but not fetch a module or a chunk. They are
// never part of the main page's bundle. The mode names the renderer:
//
//   vite build -c vite.sandbox.config.ts --mode react
//   vite build -c vite.sandbox.config.ts --mode mermaid
//
// npm run build runs both after the main build.
const renderers: Record<string, string> = {
  react: "src/sandbox/react.ts",
  mermaid: "src/sandbox/mermaid.ts",
};

export default defineConfig(({ mode }) => {
  const entry = renderers[mode];
  if (!entry) throw new Error(`vite.sandbox.config.ts: --mode is one of ${Object.keys(renderers).join(", ")}, not ${mode}`);
  return {
    // The libraries read process.env.NODE_ENV, which a browser does not have.
    define: { "process.env.NODE_ENV": JSON.stringify("production") },
    publicDir: false,
    build: {
      outDir: fileURLToPath(new URL("../dist/sandbox", import.meta.url)),
      emptyOutDir: false,
      sourcemap: false,
      copyPublicDir: false,
      lib: {
        entry: fileURLToPath(new URL(entry, import.meta.url)),
        name: `keystoneSandbox_${mode}`,
        formats: ["iife"],
        fileName: () => `${mode}.js`,
      },
    },
  };
});
