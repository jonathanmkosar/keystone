// The approval card (P7-02): a tool call that waits for the person's answer
// before it runs, drawn under the reply, with what it will do, why it asks,
// what it will be sent, and Approve and Not now. Built from the approved
// card in design/screens/MainDark.html and the design tokens.
//
// The approvals a page has seen are kept here, by chat, outside the screens,
// as turns.ts keeps the replies that are arriving. They arrive as approval
// events in a turn's stream (api.ts hands them over), and a chat's are
// forgotten when its turn ends: the step row then says what became of the
// call. docs/architecture/approvals.md has the server's side.

import { useState, useSyncExternalStore } from "react";
import { useParams } from "react-router";
import { answerApproval, ApiError, onApproval, type Approval } from "./api";
import { toolTitle } from "./steps";
import { onEnd } from "./turns";
import "./approval.css";

const byChat = new Map<string, Map<string, Approval>>();
const shots = new Map<string, Approval[]>();
const listeners = new Set<() => void>();
const none: Approval[] = [];

function put(a: Approval) {
  const kept = byChat.get(a.chat_id) ?? new Map<string, Approval>();
  kept.set(a.id, a);
  byChat.set(a.chat_id, kept);
  shots.set(a.chat_id, [...kept.values()]);
  for (const l of listeners) l();
}

onApproval(put);
onEnd((chatId) => {
  if (!byChat.delete(chatId)) return;
  shots.delete(chatId);
  for (const l of listeners) l();
});

function subscribe(fn: () => void): () => void {
  listeners.add(fn);
  return () => listeners.delete(fn);
}

/** The approvals a chat's turn has asked for, in the order they came. */
export function useApprovals(chatId: string): Approval[] {
  return useSyncExternalStore(subscribe, () => shots.get(chatId) ?? none);
}

/** True while the tool call with this id, in the chat on the screen, waits for the person. */
export function useWaitingFor(callId: string): boolean {
  const { id = "" } = useParams();
  const list = useApprovals(id);
  return list.some((a) => a.call_id === callId && a.status === "pending");
}

const answered: Record<string, string> = {
  approved: "Approved. It runs now.",
  declined: "Declined. It was not run, and the model has been told.",
  expired: "Nobody answered in time, so it was not run.",
  stopped: "The reply was stopped, so it was not run.",
};

function pretty(input: unknown): string {
  try {
    return JSON.stringify(input ?? {}, null, 2);
  } catch {
    return String(input);
  }
}

function ApprovalCard({ approval }: { approval: Approval }) {
  const [busy, setBusy] = useState(false);
  const [open, setOpen] = useState(false);
  const [problem, setProblem] = useState("");
  const input = pretty(approval.input);
  const sends = input !== "{}";
  const pending = approval.status === "pending";

  async function answer(how: "approve" | "decline") {
    setBusy(true);
    setProblem("");
    try {
      put(await answerApproval(approval, how));
    } catch (e) {
      if (e instanceof ApiError && e.status === 409) setProblem("This call was already answered, or its reply has ended.");
      else if (e instanceof ApiError && e.kind === "signed-out") setProblem("You are signed out. Sign in, then answer again.");
      else setProblem("The answer did not reach Keystone. Check that the server is running, then try again.");
    } finally {
      setBusy(false);
    }
  }

  return (
    <section className={pending ? "approval" : "approval is-answered"} aria-label={pending ? "Approval needed" : "Approval answered"}>
      <div className="approval-head">
        <span className="approval-title">{toolTitle(approval.tool, JSON.stringify(approval.input ?? {}))}</span>
        <span className="approval-why">{approval.why}</span>
      </div>
      <p className="approval-summary">{approval.summary}</p>
      {sends && (
        <>
          <button type="button" className="approval-toggle" aria-expanded={open} onClick={() => setOpen((o) => !o)}>
            {open ? "Hide what it will be sent" : "Show what it will be sent"}
          </button>
          {open && <pre className="approval-input">{input}</pre>}
        </>
      )}
      {pending ? (
        <div className="approval-actions">
          <button type="button" className="button button-primary approval-button" disabled={busy} onClick={() => void answer("approve")}>
            Approve
          </button>
          <button type="button" className="approval-later" disabled={busy} onClick={() => void answer("decline")}>
            Not now
          </button>
        </div>
      ) : (
        <p className="approval-state" role="status">
          {answered[approval.status] ?? approval.status}
        </p>
      )}
      {problem && (
        <p className="approval-problem" role="alert">
          {problem}
        </p>
      )}
    </section>
  );
}

/** The approval cards of the chat's turn, under its reply. Nothing when there are none. */
export function Approvals({ chatId }: { chatId: string }) {
  const list = useApprovals(chatId);
  if (list.length === 0) return null;
  return (
    <div className="approvals">
      {list.map((a) => (
        <ApprovalCard key={a.id} approval={a} />
      ))}
    </div>
  );
}
