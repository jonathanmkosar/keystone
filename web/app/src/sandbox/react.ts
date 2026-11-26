// The React renderer of the sandbox (P9-03): turns a React artifact's JSX
// into JavaScript with Sucrase, runs it, and draws its default export. An
// artifact may import react and react-dom, and nothing else: there is no
// network in the frame. Decision 0421.

import * as React from "react";
import * as ReactDOM from "react-dom";
import * as ReactDOMClient from "react-dom/client";
import * as JSXRuntime from "react/jsx-runtime";
import { transform } from "sucrase";
import { messageOf, readSource, root, showProblem } from "./shared";

const modules: Record<string, unknown> = {
  react: React,
  "react-dom": ReactDOM,
  "react-dom/client": ReactDOMClient,
  "react/jsx-runtime": JSXRuntime,
  "react/jsx-dev-runtime": JSXRuntime,
};

type Exports = Record<string, unknown> & { default?: unknown };

declare global {
  interface Window {
    __keystoneArtifact?: (require: (name: string) => unknown, module: { exports: Exports }, exports: Exports, React: unknown) => void;
  }
}

function require(name: string): unknown {
  if (name in modules) return modules[name];
  throw new Error(`An artifact can import react and react-dom only, and this one imports “${name}”. Nothing can be loaded from the internet in Keystone's sandbox.`);
}

/** The component to draw: the default export, or else an export named App, or else the only function exported. */
function componentOf(exports: Exports): unknown {
  if (typeof exports.default === "function" || (typeof exports.default === "object" && exports.default !== null)) return exports.default;
  if (typeof exports.App === "function") return exports.App;
  const fns = Object.values(exports).filter((v) => typeof v === "function");
  return fns.length === 1 ? fns[0] : undefined;
}

let failed = false;
window.addEventListener("error", (e) => {
  if (failed) return;
  failed = true;
  showProblem("The component stopped with an error.", messageOf(e.error ?? e.message));
});

function run() {
  const source = readSource();
  if (!source) {
    showProblem("There is nothing to draw.", "The page holds no artifact.");
    return;
  }
  let code: string;
  try {
    code = transform(source.content, {
      transforms: ["jsx", "typescript", "imports"],
      jsxRuntime: "automatic",
      production: true,
    }).code;
  } catch (e) {
    showProblem("The component could not be read.", messageOf(e));
    return;
  }
  // The component's code runs as a script of its own, so a mistake in it
  // is reported with its line, and nothing of it is evaluated as a string.
  const script = document.createElement("script");
  script.textContent = `window.__keystoneArtifact = function (require, module, exports, React) {\n${code}\n};`;
  document.head.appendChild(script);
  const fn = window.__keystoneArtifact;
  if (!fn) return; // the script failed to parse, and the error handler said why
  const module = { exports: {} as Exports };
  try {
    fn(require, module, module.exports, React);
  } catch (e) {
    showProblem("The component stopped with an error.", messageOf(e));
    return;
  }
  const Component = componentOf(module.exports);
  if (!Component) {
    showProblem("There is no component to draw.", "A React artifact exports its component as the default export: export default function App() { … }");
    return;
  }
  class Boundary extends React.Component<{ children: React.ReactNode }, { error?: unknown }> {
    state: { error?: unknown } = {};
    static getDerivedStateFromError(error: unknown) {
      return { error };
    }
    render() {
      if (this.state.error !== undefined) {
        return React.createElement("div", { className: "ks-note" }, React.createElement("strong", null, "The component stopped with an error."), React.createElement("pre", { className: "ks-text" }, messageOf(this.state.error)));
      }
      return this.props.children;
    }
  }
  ReactDOMClient.createRoot(root()).render(React.createElement(Boundary, null, React.createElement(Component as React.ElementType)));
}

run();
