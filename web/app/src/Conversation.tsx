// The conversation as it is drawn: the person's messages as bubbles, and each
// reply as its steps (thinking and tool calls), its text, and any error.
// This file only draws. layout.ts turns the record and a reply still
// arriving into the items drawn here.

import { Fragment, useState, type KeyboardEvent, type MouseEvent } from "react";
import { useNarrow } from "./PhoneSheet";
import { useParams } from "react-router";
import type { Block } from "./api";
import { useWaitingFor } from "./Approval";
import { ArtifactCard, isArtifactCard } from "./ArtifactCard";
import { ReplyNotices, SaidMedia } from "./Attachments";
import { useChatUploads } from "./uploads";
import { Icon } from "./Icon";
import { CopyButton, Markdown } from "./Markdown";
import { InlineVisuals } from "./InlineVisuals";
import { LocationMark } from "./LocationControl";
import { InspectButton, PinButton, SaidActions } from "./MessageActions";
import { errorLabel, toolLine } from "./steps";
import { CitationSources, sourcesOf } from "./citations";
import { TaskNote } from "./Tasks";
import { isSubagentsStep, SubagentLanes } from "./Subagents";
import { BrowserShot } from "./BrowserShot";
import { ReplySpinner } from "./Spinner";

export type Step =
  | { kind: "thinking"; text: string; running: boolean; seconds?: number }
  | { kind: "tool"; id: string; name: string; input: string; running: boolean; result?: string; failed?: boolean };

export type Part =
  | { kind: "steps"; steps: Step[] }
  | { kind: "text"; text: string }
  | { kind: "error"; text: string }
  | { kind: "note"; text: string }
  // Keystone's own note at the end of a reply that reached the turn's step limit.
  | { kind: "limit"; text: string }
  // A background task's note, with its result (P8-01, Tasks.tsx).
  | { kind: "task"; text: string; messageId: number; chatId: string };

export type Item =
  // id is the message's, for its Pin action (P5-04). blocks are its content,
  // for its images and files (P9-02). located says it went with the person's
  // location, and place is the line the model was given (P10-06).
  | { kind: "user"; key: string; text: string; files: string[]; id?: number; blocks?: Block[]; located?: boolean; place?: string }
  // turnId is the turn that wrote the reply, for Inspect, and pinId the
  // message of it a pin goes on: its last with text (P5-04).
  | { kind: "reply"; key: string; parts: Part[]; streaming: boolean; turnId?: string; pinId?: number };

function seconds(n: number): string {
  const whole = Math.max(1, Math.round(n));
  return whole === 1 ? "1 second" : `${whole} seconds`;
}

function ThinkingStep({ step }: { step: Extract<Step, { kind: "thinking" }> }) {
  const [open, setOpen] = useState(false);
  const label = step.running ? "Thinking" : step.seconds !== undefined ? `Thought for ${seconds(step.seconds)}` : "Thought";
  // A model may keep its thinking to itself. Then there is nothing to open.
  if (step.text === "") {
    return (
      <div className={step.running ? "step is-running" : "step"}>
        <Icon name="caret" size={16} />
        {step.running ? label : `${label}. The model kept it to itself.`}
      </div>
    );
  }
  return (
    <>
      <button type="button" className={step.running ? "step step-button is-running" : "step step-button"} aria-expanded={open} onClick={() => setOpen((o) => !o)}>
        <span className={open ? "step-caret is-open" : "step-caret"}>
          <Icon name="caret" size={16} />
        </span>
        {label}
      </button>
      {open && <div className="step-body step-thinking">{step.text}</div>}
    </>
  );
}

function pretty(json: string): string {
  try {
    return JSON.stringify(JSON.parse(json), null, 2);
  } catch {
    return json;
  }
}

// One tool call: a readable line of what it is doing or did, a few words on
// the right about what came back, and, opened, what it was sent and what it
// gave back. It is closed until the person opens it. steps.ts writes the line.
function ToolStep({ step }: { step: Extract<Step, { kind: "tool" }> }) {
  const [open, setOpen] = useState(false);
  const input = step.input.trim();
  const sent = input !== "" && input !== "{}";
  const line = toolLine(step.name, input, step.result);
  const error = step.failed ? errorLabel(step.result) : undefined;
  // A call that waits for the person's approval has not started (P7-02).
  const waiting = useWaitingFor(step.id) && step.running;
  // A step that gave no result says what the model asked for, not what was done.
  const label = waiting ? line.asked : step.running ? line.running : error ? line.asked : line.done;
  const className = ["step", "step-tool", step.running && !waiting ? "is-running" : ""].filter(Boolean).join(" ");
  const end = waiting ? (
    <span className="step-meta">waiting for you</span>
  ) : step.running ? (
    <span className="step-meta">running</span>
  ) : error ? (
    <span className={error.broke ? "step-tag step-tag-error" : "step-tag"}>{error.text}</span>
  ) : line.meta !== "" ? (
    <span className="step-meta">{line.meta}</span>
  ) : null;
  const body = (
    <>
      <Icon name={line.icon} size={16} />
      <span className="step-label" title={step.name}>
        {label}
      </span>
      {end}
    </>
  );
  if (!sent && step.result === undefined) {
    return (
      <div className={className} role={step.running ? "status" : undefined}>
        {body}
      </div>
    );
  }
  return (
    <>
      <button type="button" className={className + " step-button"} aria-expanded={open} onClick={() => setOpen((o) => !o)}>
        {body}
        <span className={open ? "step-caret is-open" : "step-caret"}>
          <Icon name="caret" size={16} />
        </span>
      </button>
      {open && (
        <div className="step-body">
          <div className="step-heading">Tool</div>
          <div className="step-name">{step.name}</div>
          {sent && (
            <>
              <div className="step-heading">Sent</div>
              <pre className="step-pre">{pretty(input)}</pre>
            </>
          )}
          {step.result !== undefined && (
            <>
              <div className="step-heading">{step.failed ? "Came back with an error" : "Came back"}</div>
              <pre className="step-pre">{step.result}</pre>
            </>
          )}
        </div>
      )}
      <BrowserShot name={step.name} result={step.result} failed={step.failed} />
    </>
  );
}

function Steps({ steps }: { steps: Step[] }) {
  return (
    <div className="steps">
      {steps.map((s, i) => (s.kind === "thinking" ? <ThinkingStep key={i} step={s} /> : isArtifactCard(s) ? <ArtifactCard key={s.id || i} step={s} /> : isSubagentsStep(s) ? <SubagentLanes key={s.id || i} step={s} /> : <ToolStep key={s.id || i} step={s} />))}
    </div>
  );
}

function Reply({ item }: { item: Extract<Item, { kind: "reply" }> }) {
  const { id: chatId = "" } = useParams();
  const text = item.parts
    .filter((p) => p.kind === "text")
    .map((p) => p.text)
    .join("\n\n");
  const empty = item.parts.length === 0;
  return (
    <div className="reply" aria-busy={item.streaming}>
      {item.parts.map((p, i) => {
        if (p.kind === "steps") return <Steps key={i} steps={p.steps} />;
        if (p.kind === "text") {
          const last = i === item.parts.length - 1;
          return (
            <div key={i} className={item.streaming && last ? "reply-text is-streaming" : "reply-text"}>
              {/* A chart or a diagram in the reply is drawn (P9-04); a block still arriving is waited for. */}
              <InlineVisuals streaming={item.streaming && last}>
                <Markdown text={p.text} />
              </InlineVisuals>
            </div>
          );
        }
        if (p.kind === "note") {
          return (
            <p key={i} className="reply-note">
              {p.text}
            </p>
          );
        }
        if (p.kind === "task") return <TaskNote key={i} chatId={p.chatId} messageId={p.messageId} text={p.text} />;
        if (p.kind === "limit") {
          return (
            <div key={i} className="reply-limit" role="note">
              <Icon name="stop" size={16} />
              <span>{p.text}</span>
            </div>
          );
        }
        return (
          <div key={i} className="reply-error" role="alert">
            <Icon name="alert" size={18} />
            <span>{p.text}</span>
          </div>
        );
      })}
      {/* While the turn runs, the turning mark sits at the reply's foot. With
          nothing written yet it says so; after that the steps and the text
          say what is happening, and the mark only shows it is not done. */}
      {item.streaming && <ReplySpinner words={empty ? "Thinking" : undefined} />}
      {!item.streaming && (text !== "" || item.turnId) && (
        <div className="reply-actions">
          {text !== "" && <CopyButton text={text} />}
          {chatId !== "" && item.pinId !== undefined && <PinButton chatId={chatId} messageId={item.pinId} />}
          {chatId !== "" && item.turnId && <InspectButton chatId={chatId} turnId={item.turnId} />}
        </div>
      )}
    </div>
  );
}

// A reply, with the sources its web steps read, so each [n] in its text
// links to one (citations.tsx).
function CitedReply({ item }: { item: Extract<Item, { kind: "reply" }> }) {
  return (
    <CitationSources sources={sourcesOf(item.parts)}>
      <Reply item={item} />
    </CitationSources>
  );
}

// A person's message: a bubble on the right, with Copy and Pin under it. On a
// phone they wait for a tap on the bubble, and a second tap hides them again
// (P10-07, decision 0612); a tap on a file or a link in it only opens that.
function Said({ chatId, item, known }: { chatId: string; item: Extract<Item, { kind: "user" }>; known: ReturnType<typeof useChatUploads> }) {
  const narrow = useNarrow();
  const [tapped, setTapped] = useState(false);
  const acts = chatId !== "" && item.id !== undefined;
  const tappable = narrow && acts;
  function tap(e: MouseEvent<HTMLDivElement>) {
    if ((e.target as HTMLElement).closest("a, button")) return;
    setTapped((t) => !t);
  }
  function key(e: KeyboardEvent<HTMLDivElement>) {
    if (e.target !== e.currentTarget || (e.key !== "Enter" && e.key !== " ")) return;
    e.preventDefault();
    setTapped((t) => !t);
  }
  return (
    <div className={["said-wrap", narrow ? "is-phone" : "", tapped ? "is-shown" : ""].filter(Boolean).join(" ")}>
      <div className="said" role={tappable ? "button" : undefined} tabIndex={tappable ? 0 : undefined} aria-expanded={tappable ? tapped : undefined} onClick={tappable ? tap : undefined} onKeyDown={tappable ? key : undefined}>
        <SaidMedia chatId={chatId} blocks={item.blocks} files={item.files} known={known} />
        {item.text}
      </div>
      {item.located && <LocationMark title={item.place} />}
      {acts && <SaidActions chatId={chatId} messageId={item.id!} text={item.text} phone={narrow} tapped={tapped} />}
    </div>
  );
}

/** Every message on the current branch, oldest first, then a reply still arriving. */
export function Conversation({ items }: { items: Item[] }) {
  const { id: chatId = "" } = useParams();
  const known = useChatUploads(chatId); // the chat's uploads and the notices on its replies (P9-02)
  return (
    <div className="conversation">
      {items.map((item) =>
        item.kind === "user" ? (
          <Said key={item.key} chatId={chatId} item={item} known={known} />
        ) : (
          <Fragment key={item.key}>
            <ReplyNotices notices={known.notices.filter((n) => item.turnId !== undefined && n.turn_id === item.turnId)} />
            <CitedReply item={item} />
          </Fragment>
        ),
      )}
    </div>
  );
}
