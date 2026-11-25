import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import react from "@vitejs/plugin-react";
import { defineConfig, type Plugin } from "vite";

// The design tokens are the one source of every colour. This plugin turns
// design/tokens-draft.json into a stylesheet of CSS variables at build time,
// so no colour value is written by hand in the app.
const tokensFile = fileURLToPath(new URL("../../design/tokens-draft.json", import.meta.url));
const tokensId = "virtual:tokens.css";

function block(selector: string, theme: Record<string, string>, scheme: string, indent = ""): string {
  const lines = Object.entries(theme).map(([name, value]) => `${indent}  --${name}: ${value};`);
  return `${indent}${selector} {\n${indent}  color-scheme: ${scheme};\n${lines.join("\n")}\n${indent}}`;
}

function tokens(): Plugin {
  return {
    name: "keystone-tokens",
    resolveId(id) {
      return id === tokensId ? "\0" + tokensId : undefined;
    },
    load(id) {
      if (id !== "\0" + tokensId) return undefined;
      const { themes } = JSON.parse(readFileSync(tokensFile, "utf8")) as {
        themes: { dark: Record<string, string>; light: Record<string, string> };
      };
      // Dark leads. Light applies when the system asks for it and the person
      // has not chosen, or when the person chose light.
      return [
        block(":root", themes.dark, "dark"),
        "@media (prefers-color-scheme: light) {",
        block(":root:not([data-theme])", themes.light, "light", "  "),
        "}",
        block(':root[data-theme="light"]', themes.light, "light"),
        block(':root[data-theme="dark"]', themes.dark, "dark"),
      ].join("\n") + "\n";
    },
  };
}

export default defineConfig({
  plugins: [react(), tokens()],
  build: {
    outDir: "../dist",
    emptyOutDir: true,
    sourcemap: false,
    // The CSS keeps one fixed name at the root. The page is the same either
    // way, and the server's page-files test in cmd/keystone asks for it there.
    rolldownOptions: {
      output: {
        assetFileNames: (info) =>
          info.names?.some((n) => n.endsWith(".css")) ? "keystone.css" : "assets/[name]-[hash][extname]",
      },
    },
  },
  server: {
    // In development Vite serves the app and passes API calls to Keystone.
    proxy: { "/api": "http://127.0.0.1:7900" },
  },
});
