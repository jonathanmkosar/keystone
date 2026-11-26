// The artifact panel (P9-03): an artifact beside its chat, opened from its
// card in a reply. A version picker, the artifact drawn or its source, a
// comparison of two versions line by line, and Restore, Download, Copy and
// the publish link. There is no mockup for it: built from the tokens and
// the patterns of the chat screen and the inspector, and provisional until
// the workspace mockups (P1-06). docs/architecture/artifacts.md has the
// server's side.

import { useCallback, useEffect, useRef, useState } from "react";
import { ApiError } from "./api";
import { ArtifactCode, ArtifactView } from "./ArtifactFrame";
import {
  closeArtifact,
  compareVersions,
  downloadURL,
  publishLink,
  readArtifact,
  readVersion,
  restoreVersion,
  size,
  typeLabel,
  useOpenArtifact,
  when,
  type ArtifactOne,
  type Comparison,
  type VersionRead,
} from "./artifacts";
import { Icon } from "./Icon";
import { onEnd } from "./turns";
import "./artifacts.css";

type View = "preview" | "source" | "compare";

/** The panel for this chat, when an artifact of it is open. Nothing otherwise. */
export function ArtifactPanel({ chatId }: { chatId: string }) {
  const open = useOpenArtifact(chatId);
  if (!open) return null;
  return (
    <aside className="artifact-panel" aria-labelledby="artifact-title">
      <ArtifactBody key={`${open.artifactId}-${open.asked}`} artifactId={open.artifactId} version={open.version} chatId={chatId} onClose={closeArtifact} />
    </aside>
  );
}

type Loaded = { state: "loading" } | { state: "ready"; one: ArtifactOne } | { state: "missing" } | { state: "failed" };

/** An artifact with its versions and actions: the panel's body, and the publish page's. */
export function ArtifactBody({ artifactId, version, chatId, onClose }: { artifactId: string; version?: number; chatId?: string; onClose?: () => void }) {
  const [loaded, setLoaded] = useState<Loaded>({ state: "loading" });
  const [picked, setPicked] = useState<number | undefined>(version);
  const [shown, setShown] = useState<VersionRead | undefined>();
  const [view, setView] = useState<View>("preview");
  const [against, setAgainst] = useState<number | undefined>();
  const [cmp, setCmp] = useState<Comparison | undefined>();
  const [problem, setProblem] = useState("");
  const [said, setSaid] = useState("");
  const heading = useRef<HTMLHeadingElement>(null);

  const load = useCallback(async () => {
    try {
      const one = await readArtifact(artifactId);
      setLoaded({ state: "ready", one });
    } catch (e) {
      setLoaded({ state: e instanceof ApiError && e.kind === "missing" ? "missing" : "failed" });
    }
  }, [artifactId]);

  useEffect(() => {
    void load();
  }, [load]);
  useEffect(() => {
    heading.current?.focus();
  }, []);
  // A turn of the chat may have written a new version.
  useEffect(() => {
    if (!chatId) return;
    const off = onEnd((c) => {
      if (c === chatId) void load();
    });
    return () => {
      off();
    };
  }, [chatId, load]);
  useEffect(() => {
    if (!onClose) return;
    const key = (e: KeyboardEvent) => {
      if (e.key === "Escape") onClose();
    };
    window.addEventListener("keydown", key);
    return () => window.removeEventListener("keydown", key);
  }, [onClose]);

  const one = loaded.state === "ready" ? loaded.one : undefined;
  const latest = one?.artifact.latest;
  // Unpicked, the panel follows the newest version.
  const current = picked ?? latest;

  useEffect(() => {
    if (!current) return;
    let live = true;
    readVersion(artifactId, current).then(
      (v) => live && setShown(v),
      () => live && setProblem("That version could not be read. It may no longer be kept."),
    );
    return () => {
      live = false;
    };
  }, [artifactId, current, latest]);

  // The comparison: the version shown against an older one, the one before it unless picked.
  const versions = one?.versions ?? [];
  const older = against ?? versions.filter((v) => current !== undefined && v.version < current).at(-1)?.version;
  useEffect(() => {
    if (view !== "compare" || !current || !older) {
      setCmp(undefined);
      return;
    }
    let live = true;
    compareVersions(artifactId, older, current).then(
      (c) => live && setCmp(c),
      () => live && setProblem("The two versions could not be compared."),
    );
    return () => {
      live = false;
    };
  }, [artifactId, view, older, current]);

  async function restore() {
    if (!current) return;
    setProblem("");
    try {
      const restored = await restoreVersion(artifactId, current);
      setPicked(undefined);
      setSaid(`Version ${current} is now version ${restored.version.version}, the newest. Every version is kept.`);
      await load();
    } catch (e) {
      setProblem(e instanceof ApiError && e.said ? e.said : "The version was not restored. Check that the server is running, then try again.");
    }
  }

  async function copyLink() {
    if (!one) return;
    try {
      await navigator.clipboard.writeText(publishLink(one.artifact));
      setSaid("The link is copied. It opens only for you, signed in to this Keystone.");
    } catch {
      setSaid(publishLink(one.artifact));
    }
  }

  const a = one?.artifact;
  const v = shown && shown.version.version === current ? shown : undefined;
  return (
    <>
      <header className="artifact-head">
        <Icon name="artifacts" size={18} />
        <div className="artifact-heading">
          <h2 id="artifact-title" className="artifact-title" tabIndex={-1} ref={heading}>
            {v?.version.title ?? a?.title ?? "Artifact"}
          </h2>
          {a && <span className="artifact-kind">{typeLabel(a.type, a.language)}</span>}
        </div>
        {a?.sensitive && <span className="artifact-pill is-sensitive">sensitive</span>}
        {onClose && (
          <button type="button" className="icon-button" aria-label="Close the artifact" onClick={onClose}>
            <Icon name="close" size={18} />
          </button>
        )}
      </header>
      {loaded.state === "loading" && (
        <p className="quiet artifact-state" aria-busy="true">
          Opening the artifact.
        </p>
      )}
      {loaded.state === "missing" && <p className="quiet artifact-state">This artifact is not in your record. It may have been deleted with its chat.</p>}
      {loaded.state === "failed" && (
        <div className="artifact-state">
          <p className="quiet">Keystone did not answer. Check that the server is running, then try again.</p>
          <button type="button" className="button" onClick={() => void load()}>
            <Icon name="retry" size={16} />
            Try again
          </button>
        </div>
      )}
      {one && a && current && (
        <>
          {one.notice && (
            <p className="artifact-notice" role="note">
              {one.notice}
            </p>
          )}
          <div className="artifact-bar">
            <label className="artifact-version">
              <select aria-label="Version" value={current} onChange={(e) => setPicked(Number(e.target.value) === latest ? undefined : Number(e.target.value))}>
                {[...versions].reverse().map((ver) => (
                  <option key={ver.version} value={ver.version}>
                    {`Version ${ver.version}${ver.version === latest ? " (newest)" : ""}${ver.origin === "restore" ? `, restored from ${ver.restored_from}` : ""}`}
                  </option>
                ))}
              </select>
            </label>
            <div className="artifact-tabs" role="tablist" aria-label="How the artifact is shown">
              {(["preview", "source", "compare"] as View[]).map((t) => (
                <button key={t} type="button" role="tab" aria-selected={view === t} className={view === t ? "artifact-tab is-on" : "artifact-tab"} onClick={() => setView(t)} disabled={t === "compare" && versions.length < 2}>
                  {t === "preview" ? "Preview" : t === "source" ? "Source" : "Compare"}
                </button>
              ))}
            </div>
          </div>
          <div className="artifact-actions">
            {current !== latest && (
              <button type="button" className="button button-primary" onClick={() => void restore()}>
                <Icon name="retry" size={16} />
                Restore this version
              </button>
            )}
            <a className="copy-button" href={downloadURL(a.id, current)} download>
              <Icon name="download" size={16} />
              Download
            </a>
            {v && <CopyContent text={v.content} />}
            <button type="button" className="copy-button" onClick={() => void copyLink()}>
              <Icon name="link" size={16} />
              Copy link
            </button>
            {onClose && (
              <a className="copy-button" href={a.link} target="_blank" rel="noopener">
                <Icon name="open" size={16} />
                Open alone
              </a>
            )}
          </div>
          {v && (
            <p className="artifact-meta">
              {`${v.version.origin === "restore" ? `Restored from version ${v.version.restored_from}` : "Written by the model"} · ${when(v.version.created)} · ${size(v.version.size)}`}
            </p>
          )}
          {said && (
            <p className="artifact-said" role="status">
              {said}
            </p>
          )}
          {problem && (
            <p className="artifact-problem" role="alert">
              {problem}
            </p>
          )}
          <div className={view === "preview" ? "artifact-stage is-preview" : "artifact-stage"}>
            {!v && <p className="quiet">Reading the version.</p>}
            {v && view === "preview" && <ArtifactView artifact={a} version={v.version} content={v.content} sandbox={one.sandbox} />}
            {v && view === "source" && <ArtifactCode artifact={a} content={v.content} />}
            {v && view === "compare" && (
              <div className="artifact-compare">
                <label className="artifact-against">
                  Compare version {current} with
                  <select value={older ?? ""} onChange={(e) => setAgainst(Number(e.target.value))}>
                    {versions
                      .filter((ver) => ver.version !== current)
                      .reverse()
                      .map((ver) => (
                        <option key={ver.version} value={ver.version}>
                          {`version ${ver.version}`}
                        </option>
                      ))}
                  </select>
                </label>
                {cmp ? <DiffView cmp={cmp} /> : <p className="quiet">Comparing.</p>}
              </div>
            )}
          </div>
        </>
      )}
    </>
  );
}

function CopyContent({ text }: { text: string }) {
  const [done, setDone] = useState(false);
  async function copy() {
    try {
      await navigator.clipboard.writeText(text);
      setDone(true);
      window.setTimeout(() => setDone(false), 1500);
    } catch {
      // Nothing to do: the source is on the Source tab to select by hand.
    }
  }
  return (
    <button type="button" className="copy-button" onClick={() => void copy()} aria-live="polite">
      <Icon name={done ? "check" : "copy"} size={16} />
      {done ? "Copied" : "Copy"}
    </button>
  );
}

/** Two versions, line by line: the lines both hold, those put in and those taken out. */
function DiffView({ cmp }: { cmp: Comparison }) {
  if (cmp.added === 0 && cmp.removed === 0) return <p className="quiet">{`Versions ${cmp.from} and ${cmp.to} read the same.`}</p>;
  return (
    <>
      <p className="artifact-meta">{`From version ${cmp.from} to version ${cmp.to}: ${cmp.added} ${cmp.added === 1 ? "line" : "lines"} put in, ${cmp.removed} taken out.`}</p>
      <div className="artifact-diff" role="table" aria-label={`Version ${cmp.from} compared with version ${cmp.to}`}>
        {cmp.lines.map((l, i) => (
          <div key={i} role="row" className={`diff-line is-${l.op}`}>
            <span role="cell" className="diff-num">
              {l.from ?? ""}
            </span>
            <span role="cell" className="diff-num">
              {l.to ?? ""}
            </span>
            <span role="cell" className="diff-mark" aria-label={l.op === "add" ? "put in" : l.op === "remove" ? "taken out" : "the same"}>
              {l.op === "add" ? "+" : l.op === "remove" ? "−" : " "}
            </span>
            <span role="cell" className="diff-text">
              {l.text}
            </span>
          </div>
        ))}
      </div>
    </>
  );
}
