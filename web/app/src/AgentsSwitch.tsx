// The switch that pauses every agent and schedule (P8-05), drawn where
// background tasks and schedules are shown: on the Scheduled screen, and
// above the task cards in a chat. Settings → General draws the same setting,
// agents.paused, from the registry. Built from the design tokens: there is
// no mockup for it yet (P1-05). docs/architecture/unattended-spend.md has
// the server's side.
//
// The switch is the person's, and the screens that show it are told when it
// changes here, so a list of tasks or schedules can be read again.

import { useEffect, useState, useSyncExternalStore } from "react";
import { ApiError, getAgentsSwitch, setAgentsPaused, type AgentsSwitchState } from "./api";
import { Icon } from "./Icon";
import "./agents-switch.css";

let known: AgentsSwitchState | null = null;
const listeners = new Set<() => void>();

function keep(s: AgentsSwitchState) {
  known = s;
  for (const l of listeners) l();
}

function subscribe(fn: () => void): () => void {
  listeners.add(fn);
  return () => listeners.delete(fn);
}

/** The switch as this page last read it, or null before it has. */
export function useAgentsSwitch(): AgentsSwitchState | null {
  const s = useSyncExternalStore(subscribe, () => known);
  useEffect(() => {
    getAgentsSwitch().then(keep, () => {});
  }, []);
  return s;
}

function failure(e: unknown): string {
  if (e instanceof ApiError && e.kind === "signed-out") return "You are signed out. Sign in, then try again.";
  return "The switch did not change. Try again.";
}

/**
 * The bar with the switch. `quiet` draws it only while every agent is paused
 * (the chat screen, where it sits above the task cards); otherwise it is
 * drawn both ways. `onChange` is called after the switch changed here.
 */
export function AgentsSwitch({ quiet = false, onChange }: { quiet?: boolean; onChange?: () => void }) {
  const state = useAgentsSwitch();
  const [busy, setBusy] = useState(false);
  const [problem, setProblem] = useState("");

  async function flip(on: boolean) {
    setBusy(true);
    setProblem("");
    try {
      keep(await setAgentsPaused(on));
      onChange?.();
    } catch (e) {
      setProblem(failure(e));
    }
    setBusy(false);
  }

  if (!state || (quiet && !state.paused)) return null;
  const words = state.paused
    ? `Every agent is paused: background tasks and scheduled runs wait where they stopped, and no schedule starts a run. Your own chats go on.${state.working > 0 ? ` ${state.working} still stopping.` : ""}`
    : "Background tasks and scheduled runs go on by themselves. Pausing stops them all within seconds; nothing is lost, and they go on when you resume.";
  return (
    <div className={state.paused ? "agents-switch is-paused" : "agents-switch"} role="status">
      <Icon name="agents" size={16} />
      <p className="agents-switch-words">{words}</p>
      <button type="button" className={state.paused ? "button button-primary" : "button"} disabled={busy} onClick={() => void flip(!state.paused)}>
        {busy ? (state.paused ? "Resuming" : "Pausing") : state.paused ? "Resume all agents" : "Pause all agents"}
      </button>
      {problem && (
        <p className="agents-switch-problem" role="alert">
          {problem}
        </p>
      )}
    </div>
  );
}
