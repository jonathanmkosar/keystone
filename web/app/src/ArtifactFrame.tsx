// How an artifact is drawn (P9-03). Markdown with the app's own Markdown
// reader and code as a code block, both in the app, since neither runs
// anything. HTML, React, SVG and Mermaid in an iframe whose page the server
// serves under a policy of its own (internal/artifacts/sandbox.go), with a
// sandbox that never allows same-origin: the frame's script can never reach
// the person's session, cookies or API. The frame may have only the
// permissions in frameAllowed, whatever the server says; verify's artifacts
// check reads this file to hold that.

import { CopyButton, Markdown } from "./Markdown";
import { sandboxURL, typeLabel, usePageTheme, type Artifact, type ArtifactVersion } from "./artifacts";

/** The only permissions an artifact's frame may be given. */
const frameAllowed = ["allow-scripts", "allow-forms", "allow-modals", "allow-downloads", "allow-pointer-lock"];

/** The frame's sandbox attribute: the server's permissions that are in frameAllowed. */
export function frameSandbox(fromServer: string): string {
  return fromServer
    .split(/\s+/)
    .filter((p) => frameAllowed.includes(p))
    .join(" ");
}

/** Whether a type is drawn in the sandboxed frame. */
export function inFrame(type: string): boolean {
  return type === "html" || type === "react" || type === "svg" || type === "mermaid";
}

export function ArtifactCode({ artifact, content }: { artifact: Artifact; content: string }) {
  const lang = artifact.type === "code" ? artifact.language || "text" : artifact.type === "react" ? "jsx" : artifact.type;
  return (
    <div className="code-block artifact-code">
      <div className="code-head">
        <span className="code-lang">{lang}</span>
        <CopyButton text={content} />
      </div>
      <pre>
        <code>{content}</code>
      </pre>
    </div>
  );
}

/** One version of an artifact, drawn as its type is. */
export function ArtifactView({ artifact, version, content, sandbox }: { artifact: Artifact; version: ArtifactVersion; content: string; sandbox: string }) {
  const theme = usePageTheme();
  if (artifact.type === "markdown") {
    return (
      <div className="artifact-doc">
        <Markdown text={content} />
      </div>
    );
  }
  if (!inFrame(artifact.type)) return <ArtifactCode artifact={artifact} content={content} />;
  return (
    <iframe
      key={`${artifact.id}-${version.version}-${theme}`}
      className={`artifact-frame is-${artifact.type}`}
      title={`${version.title}, ${typeLabel(artifact.type, artifact.language)}, version ${version.version}`}
      src={sandboxURL(artifact.id, version.version, theme)}
      sandbox={frameSandbox(sandbox)}
      referrerPolicy="no-referrer"
      loading="lazy"
    />
  );
}
