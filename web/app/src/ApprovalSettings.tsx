// The Approvals settings screen (P7-02): each tool a turn may use, with the
// person's policy for it: always, ask or never. Every call is in api.ts.
// The rule that no policy can change is said on the screen: a tool that
// changes something asks once the chat has read outside content.

import { useEffect, useState } from "react";
import { ApiError, listToolPolicies, resetToolPolicy, setToolPolicy, type ToolPolicy } from "./api";
import { Icon } from "./Icon";
import "./approval.css";

const policyWords: Record<string, string> = {
  always: "Always",
  ask: "Ask",
  never: "Never",
};

function kind(t: ToolPolicy): string {
  const acts = t.read_only ? "Only reads." : "Changes something.";
  const from = t.inside ? "Its result is Keystone's own." : "Its result can be outside content.";
  return `${acts} ${from}`;
}

function ToolRow({ tool, policies, onChanged, onSignedOut }: { tool: ToolPolicy; policies: string[]; onChanged: (t: ToolPolicy) => void; onSignedOut: () => void }) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const id = `policy-${tool.name}`;

  async function run(change: () => Promise<ToolPolicy>) {
    setBusy(true);
    setError("");
    try {
      onChanged(await change());
    } catch (e) {
      if (e instanceof ApiError && e.kind === "signed-out") onSignedOut();
      else setError("The policy could not be changed. Check that the server is running.");
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="set-item" data-tool={tool.name}>
      <div className="set-item-main">
        <label className="set-item-title" htmlFor={id}>
          <span className="set-mono">{tool.name}</span>
        </label>
        <span className="set-hint">
          {kind(tool)} {tool.description}
        </span>
        {error && (
          <span className="form-error" role="alert">
            {error}
          </span>
        )}
      </div>
      <div className="set-control">
        <select id={id} className="input policy-select" value={tool.policy} disabled={busy} onChange={(e) => void run(() => setToolPolicy(tool.name, e.target.value))}>
          {policies.map((p) => (
            <option key={p} value={p}>
              {policyWords[p] ?? p}
            </option>
          ))}
        </select>
        {tool.overridden ? (
          <button type="button" className="set-reset" disabled={busy} onClick={() => void run(() => resetToolPolicy(tool.name))}>
            Reset to default: {policyWords[tool.default] ?? tool.default}
          </button>
        ) : (
          <span className="set-hint">Default</span>
        )}
      </div>
    </div>
  );
}

export function ApprovalSettings({ onSignedOut }: { onSignedOut: () => void }) {
  const [state, setState] = useState<{ kind: "loading" } | { kind: "failed" } | { kind: "ready"; tools: ToolPolicy[]; policies: string[] }>({ kind: "loading" });
  const [turn, setTurn] = useState(0);

  useEffect(() => {
    let live = true;
    listToolPolicies().then(
      (got) => live && setState({ kind: "ready", ...got }),
      (e) => {
        if (!live) return;
        if (e instanceof ApiError && e.kind === "signed-out") onSignedOut();
        else setState({ kind: "failed" });
      },
    );
    return () => {
      live = false;
    };
    // onSignedOut is the same function for the life of the screen.
  }, [turn]);

  return (
    <section className="set-section">
      <div className="set-section-head">
        <h2 className="set-heading">Approvals</h2>
        <p className="set-note">
          What Keystone may do without asking you. Always runs a tool without asking, Ask shows you a card first, and Never keeps the tool from the model. Whatever is set here, a tool that changes something asks
          once the chat has read outside content, such as a web page, an email or a source in your knowledge store.
        </p>
      </div>
      {state.kind === "loading" && (
        <p className="quiet" aria-busy="true">
          Loading.
        </p>
      )}
      {state.kind === "failed" && (
        <div className="set-stack">
          <p className="quiet">The tools could not be read. Check that the server is running.</p>
          <div>
            <button type="button" className="button" onClick={() => setTurn((n) => n + 1)}>
              <Icon name="retry" size={16} />
              Try again
            </button>
          </div>
        </div>
      )}
      {state.kind === "ready" && (
        <div className="set-card">
          {state.tools.map((t) => (
            <ToolRow
              key={t.name}
              tool={t}
              policies={state.policies}
              onSignedOut={onSignedOut}
              onChanged={(got) => setState((s) => (s.kind === "ready" ? { ...s, tools: s.tools.map((x) => (x.name === got.name ? got : x)) } : s))}
            />
          ))}
        </div>
      )}
    </section>
  );
}
