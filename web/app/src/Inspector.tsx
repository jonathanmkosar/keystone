// The turn inspector (P5-04): what went into each model call of one turn,
// part by part, with tokens and cost, and what the person may change for
// the chat's next turn: take a recalled item or a pinned message out of it,
// or take a pin off. Opened from a reply's Inspect action and drawn as a
// panel on the right. Built from the tokens, after the inspector button in
// the header of design/screens/MainDark.html; there is no inspector mockup,
// so the panel is provisional (P1-06). docs/architecture/inspector.md has
// the server's side.
//
// Which turn is open is kept here, outside the screens, as Approval.tsx
// keeps its cards.

import { useCallback, useEffect, useRef, useState, useSyncExternalStore } from "react";
import {
  ApiError,
  inspectTurn,
  setSkip,
  type ChatSummary,
  type InspectedCall,
  type Inspection,
  type ItemRecord,
  type PartRecord,
  type RecallHit,
  type RecallRecord,
  type Skip,
  type Totals,
  type UsageRecord,
} from "./api";
import { UploadsSection, type UploadCall } from "./Attachments";
import { Icon } from "./Icon";
import { LocationSection } from "./LocationControl";
import { pinMessage, putPins, usePins } from "./pins";
import { onEnd } from "./turns";
import "./inspector.css";

let shown: { chatId: string; turnId: string } | undefined;
const listeners = new Set<() => void>();

function tell() {
  for (const l of listeners) l();
}

function subscribe(fn: () => void): () => void {
  listeners.add(fn);
  return () => listeners.delete(fn);
}

/** Opens the inspector on one turn of a chat. */
export function openInspector(chatId: string, turnId: string) {
  shown = { chatId, turnId };
  tell();
}

/** Closes the inspector. */
export function closeInspector() {
  shown = undefined;
  tell();
}

// ---------------------------------------------------------------------------
// Words and numbers.
// ---------------------------------------------------------------------------

const partNames: Record<string, string> = {
  rules: "Standing instructions",
  tools: "Tools",
  profile: "Profile",
  person_map: "Person map",
  project: "Project",
  style: "Style",
  current: "This turn",
  attached: "Attachments",
  recent: "Earlier turns",
  recall: "Recalled knowledge",
  summary: "Summary of older turns",
  pinned: "Pinned messages",
  note: "Note on what was left out",
};

const kindNames: Record<string, string> = {
  chat: "Reply",
  title: "Title",
  summary: "Summary of older turns",
  memory: "Memory",
  embedding: "Embedding",
};

const reasons: Record<string, string> = {
  below_threshold: "below the relevance threshold",
  sensitive: "withheld: a model this call could reach may not read sensitive content",
  over_limit: "enough better hits were kept",
  unreadable: "could not be read",
};

const outcomes: Record<string, string> = {
  ok: "answered",
  stopped: "stopped by you",
  refused: "not sent",
  error: "failed",
};

function count(n: number | undefined): string {
  return (n ?? 0).toLocaleString("en-US");
}

function dollars(micros: number): string {
  if (micros === 0) return "$0";
  const d = micros / 1e6;
  return d < 0.01 ? `$${d.toFixed(4)}` : `$${d.toFixed(2)}`;
}

function seconds(ms: number): string {
  return ms < 1000 ? `${ms} ms` : `${(ms / 1000).toFixed(1)} s`;
}

function when(iso: string | undefined): string {
  if (!iso) return "";
  const d = new Date(iso);
  return Number.isNaN(d.getTime()) ? "" : d.toLocaleString(undefined, { dateStyle: "medium", timeStyle: "short" });
}

function partState(p: PartRecord): string {
  if (p.part === "pinned" && p.state === "empty") return "in their turns";
  switch (p.state) {
    case "kept":
      return "sent";
    case "partly":
      return "partly sent";
    case "dropped":
      return "left out";
  }
  return p.state;
}

function itemState(it: ItemRecord): string {
  if (it.removed) return it.in_turn ? "left out by you, in its turn all the same" : "left out by you";
  if (it.in_turn) return "in its turn";
  if (it.kept) return it.pinned && it.ref?.startsWith("message:") ? "sent on its own" : "sent";
  return "left out for room";
}

// ---------------------------------------------------------------------------
// The panel.
// ---------------------------------------------------------------------------

/** The inspector for this chat, when it is open on one of its turns. Nothing otherwise. */
export function Inspector({ chatId }: { chatId: string }) {
  const open = useSyncExternalStore(subscribe, () => shown);
  if (!open || open.chatId !== chatId) return null;
  return <Panel key={open.turnId} chatId={chatId} turnId={open.turnId} />;
}

type Loaded = { state: "loading" } | { state: "ready"; data: Inspection } | { state: "missing" } | { state: "failed" };

/** What one change for the next turn needs: an item's kind and id, and whether it is taken out. */
type SkipChange = (kind: string, ref: string, out: boolean) => void;

function Panel({ chatId, turnId }: { chatId: string; turnId: string }) {
  const [loaded, setLoaded] = useState<Loaded>({ state: "loading" });
  const [skips, setSkips] = useState<Skip[]>([]);
  const [problem, setProblem] = useState("");
  const heading = useRef<HTMLHeadingElement>(null);

  const load = useCallback(async () => {
    try {
      const data = await inspectTurn(chatId, turnId);
      setLoaded({ state: "ready", data });
      setSkips(data.skips);
      putPins(chatId, data.pinned);
    } catch (e) {
      setLoaded({ state: e instanceof ApiError && e.kind === "missing" ? "missing" : "failed" });
    }
  }, [chatId, turnId]);

  useEffect(() => {
    void load();
  }, [load]);
  useEffect(() => {
    heading.current?.focus();
  }, []);
  // A turn that ends in this chat may be this one finishing, or the next
  // one, which used what was taken out of it.
  useEffect(() => {
    const off = onEnd((c) => {
      if (c === chatId) void load();
    });
    return () => {
      off();
    };
  }, [chatId, load]);
  useEffect(() => {
    const key = (e: KeyboardEvent) => {
      if (e.key === "Escape") closeInspector();
    };
    window.addEventListener("keydown", key);
    return () => window.removeEventListener("keydown", key);
  }, []);

  const change: SkipChange = (kind, ref, out) => {
    setProblem("");
    setSkip(chatId, kind, ref, out).then(setSkips, (e: unknown) => {
      if (e instanceof ApiError && e.said) setProblem(e.said);
      else setProblem("The change did not reach Keystone. Check that the server is running, then try again.");
    });
  };

  return (
    <aside className="inspector" aria-labelledby="inspector-title">
      <header className="inspector-head">
        <Icon name="layers" size={18} />
        <h2 id="inspector-title" className="inspector-title" tabIndex={-1} ref={heading}>
          Turn inspector
        </h2>
        <button type="button" className="icon-button" aria-label="Close the turn inspector" onClick={closeInspector}>
          <Icon name="close" size={18} />
        </button>
      </header>
      <div className="inspector-body">
        {loaded.state === "loading" && (
          <p className="quiet" aria-busy="true">
            Reading the turn.
          </p>
        )}
        {loaded.state === "missing" && <p className="quiet">This turn is not in your record. It may have been deleted with its chat.</p>}
        {loaded.state === "failed" && (
          <div className="inspector-state">
            <p className="quiet">Keystone did not answer. Check that the server is running, then try again.</p>
            <button type="button" className="button" onClick={() => void load()}>
              <Icon name="retry" size={16} />
              Try again
            </button>
          </div>
        )}
        {problem && (
          <p className="inspector-problem" role="alert">
            {problem}
          </p>
        )}
        {loaded.state === "ready" && <Inspected chatId={chatId} data={loaded.data} skips={skips} change={change} />}
      </div>
    </aside>
  );
}

function Inspected({ chatId, data, skips, change }: { chatId: string; data: Inspection; skips: Skip[]; change: SkipChange }) {
  const t = data.turn;
  const chats = data.calls.filter((c) => c.kind === "chat");
  const others = data.calls.filter((c) => c.kind !== "chat");
  const out = new Set(skips.map((s) => `${s.kind}:${s.ref}`));
  const hits = new Map<string, RecallHit>();
  for (const h of data.recall?.hits ?? []) hits.set(`${h.type}:${h.id}`, h);
  const line = [when(t.created), t.status, t.model].filter(Boolean).join(" · ");
  return (
    <>
      {line && <p className="inspector-line">{line}</p>}
      <TotalsTiles totals={data.totals} />
      <NextTurn skips={skips} change={change} />
      {chats.length === 0 && <p className="quiet">This turn made no model call.</p>}
      {chats.map((c, i) => (
        <ChatCall key={c.step ?? i} chatId={chatId} call={c} many={chats.length > 1} summaries={data.summaries} hits={hits} out={out} change={change} />
      ))}
      {others.length > 0 && (
        <section className="inspector-section" aria-label="Other calls">
          <h3 className="inspector-heading">Other calls this turn made</h3>
          <ul className="inspector-list">
            {others.map((c, i) => (
              <li key={i} className="inspector-other">
                <span className="part-name">{kindNames[c.kind] ?? c.kind}</span>
                <UsageLines records={c.usage} />
              </li>
            ))}
          </ul>
        </section>
      )}
      {data.recall && <RecallSection recall={data.recall} removed={removedRefs(chats[0])} />}
      <UploadsSection calls={data.uploads as UploadCall[] | undefined} />
      <LocationSection location={data.location} />
    </>
  );
}

/** The items the person took out of a call. */
function removedRefs(call: InspectedCall | undefined): Set<string> {
  const out = new Set<string>();
  for (const p of call?.context?.parts ?? []) for (const it of p.items ?? []) if (it.removed && it.ref) out.add(it.ref);
  return out;
}

function TotalsTiles({ totals }: { totals: Totals }) {
  const tiles = [
    { label: "Tokens in", value: count(totals.input_tokens) },
    { label: "Tokens out", value: count(totals.output_tokens) },
    { label: "Cost", value: dollars(totals.cost_micros) },
    { label: "Model calls", value: count(totals.calls) },
  ];
  const more: string[] = [];
  if (totals.thinking_tokens) more.push(`${count(totals.thinking_tokens)} of the tokens out were thinking`);
  if (totals.cache_read_tokens || totals.cache_write_tokens) more.push(`${count(totals.cache_read_tokens)} read from the cache, ${count(totals.cache_write_tokens)} written to it`);
  if (totals.estimated) more.push("some counts are estimates");
  return (
    <section className="inspector-section" aria-label="This turn in all">
      <div className="inspector-tiles">
        {tiles.map((x) => (
          <div key={x.label} className="inspector-tile">
            <span className="inspector-tile-value">{x.value}</span>
            <span className="inspector-tile-label">{x.label}</span>
          </div>
        ))}
      </div>
      {more.length > 0 && <p className="inspector-note">{more.join("; ")}.</p>}
    </section>
  );
}

function NextTurn({ skips, change }: { skips: Skip[]; change: SkipChange }) {
  if (skips.length === 0) return null;
  return (
    <section className="inspector-section inspector-next" aria-label="Left out of the next turn">
      <h3 className="inspector-heading">Left out of your next message</h3>
      <ul className="inspector-list">
        {skips.map((s) => (
          <li key={`${s.kind}:${s.ref}`} className="inspector-item">
            <span className="inspector-item-label">{s.label || `${s.kind} ${s.ref}`}</span>
            <button type="button" className="copy-button" onClick={() => change(s.kind, s.ref, false)}>
              Put back
            </button>
          </li>
        ))}
      </ul>
    </section>
  );
}

function ChatCall(props: {
  chatId: string;
  call: InspectedCall;
  many: boolean;
  summaries: ChatSummary[];
  hits: Map<string, RecallHit>;
  out: Set<string>;
  change: SkipChange;
}) {
  const { call } = props;
  const rec = call.context;
  const title = props.many ? `Model call ${call.step}` : "The model call";
  const fellBack = call.usage.length > 1 ? `, after ${call.usage.length - 1 === 1 ? "one model" : `${call.usage.length - 1} models`} did not answer` : "";
  return (
    <section className="inspector-section" aria-label={title}>
      <h3 className="inspector-heading">
        {title}
        {call.model && <span className="inspector-model"> · {call.model}</span>}
      </h3>
      {fellBack && <p className="inspector-note">Answered by {call.model}{fellBack}.</p>}
      {rec ? <Parts {...props} /> : <p className="quiet">What this call was built from was not kept.</p>}
      <UsageLines records={call.usage} />
    </section>
  );
}

function Parts({ chatId, call, summaries, hits, out, change }: { chatId: string; call: InspectedCall; summaries: ChatSummary[]; hits: Map<string, RecallHit>; out: Set<string>; change: SkipChange }) {
  const rec = call.context!;
  const pins = usePins(chatId);
  // A part with items is shown even when nothing of it was sent on its own:
  // a pinned message in a turn that was sent is there all the same.
  const shown = (p: PartRecord) => p.state !== "empty" || (p.items?.length ?? 0) > 0;
  const shownParts = rec.parts.filter(shown);
  const emptyParts = rec.parts.filter((p) => !shown(p) && p.part !== "note").map((p) => partNames[p.part] ?? p.part);
  return (
    <>
      <progress className={rec.over > 0 ? "inspector-meter is-over" : "inspector-meter"} value={Math.min(rec.tokens, rec.limit)} max={Math.max(rec.limit, 1)} aria-label={`${count(rec.tokens)} of ${count(rec.limit)} tokens`} />
      <p className="inspector-note">
        {count(rec.tokens)} of {count(rec.limit)} tokens{rec.exact ? "" : ", estimated"}. The window is {count(rec.window)}, with {count(rec.output)} kept for the reply.
        {rec.over > 0 && ` It was ${count(rec.over)} over, so it was not sent.`}
      </p>
      <ul className="inspector-parts">
        {shownParts.map((p) => (
          <li key={p.part} className="inspector-part">
            <div className="inspector-part-row">
              <span className="part-name">{partNames[p.part] ?? p.part}</span>
              <span className={p.state === "dropped" ? "part-state is-out" : "part-state"}>{partState(p)}</span>
              <span className="part-tokens">{count(p.tokens)}</span>
            </div>
            <PartDetail chatId={chatId} part={p} call={call} summaries={summaries} hits={hits} out={out} pins={pins} change={change} />
          </li>
        ))}
      </ul>
      {emptyParts.length > 0 && <p className="inspector-note">Nothing in: {emptyParts.join(", ")}.</p>}
    </>
  );
}

function PartDetail(props: {
  chatId: string;
  part: PartRecord;
  call: InspectedCall;
  summaries: ChatSummary[];
  hits: Map<string, RecallHit>;
  out: Set<string>;
  pins: ReadonlySet<number>;
  change: SkipChange;
}) {
  const { part: p, call } = props;
  switch (p.part) {
    case "tools":
      return <p className="inspector-note">{p.kept === 1 ? "One tool offered." : `${count(p.kept)} tools offered.`}</p>;
    case "recent":
      return <EarlierTurns call={call} part={p} />;
    case "recall":
      return <Items {...props} />;
    case "attached": // the text read from the chat's files (P9-02)
      return <Items {...props} />;
    case "pinned":
      return <Items {...props} />;
    case "summary":
      return <SummaryDetail part={p} summaries={props.summaries} />;
    case "note":
      return <p className="inspector-note">The model was told that earlier turns were left out.</p>;
  }
  if (p.dropped > 0) return <p className="inspector-note">{count(p.dropped_tokens)} tokens left out.</p>;
  return null;
}

function Items({ chatId, part, hits, out, pins, change }: { chatId: string; part: PartRecord; hits: Map<string, RecallHit>; out: Set<string>; pins: ReadonlySet<number>; change: SkipChange }) {
  const [problem, setProblem] = useState("");
  const items = part.items ?? [];
  if (items.length === 0) return null;
  async function unpin(id: number) {
    setProblem("");
    try {
      await pinMessage(chatId, id, false);
    } catch {
      setProblem("The pin could not be taken off. Check that the server is running, then try again.");
    }
  }
  return (
    <>
      <ul className="inspector-items">
        {items.map((it, i) => {
          const [kind, ref] = splitRef(it.ref);
          const hit = it.ref ? hits.get(it.ref) : undefined;
          const message = kind === "message" ? Number(ref) : 0;
          const leaving = it.ref ? out.has(it.ref) : false;
          const canLeave = kind === "page" || kind === "source" || (message > 0 && pins.has(message));
          return (
            <li key={it.ref || i} className={it.removed ? "inspector-item is-removed" : "inspector-item"}>
              <div className="inspector-item-text">
                <span className="inspector-item-label">{it.label || it.ref || "an item"}</span>
                <span className="inspector-item-meta">
                  {itemState(it)} · {count(it.tokens)} tokens
                  {hit && ` · score ${hit.score.toFixed(2)}${hit.sensitive ? " · sensitive" : ""}`}
                </span>
              </div>
              <div className="inspector-item-actions">
                {canLeave && it.ref && (
                  <button type="button" className={leaving ? "copy-button action-on" : "copy-button"} aria-pressed={leaving} onClick={() => change(kind, ref, !leaving)}>
                    {leaving ? "Left out next turn" : "Leave out next turn"}
                  </button>
                )}
                {message > 0 && pins.has(message) && (
                  <button type="button" className="copy-button" onClick={() => void unpin(message)}>
                    <Icon name="pin" size={14} />
                    Unpin
                  </button>
                )}
              </div>
            </li>
          );
        })}
      </ul>
      {problem && (
        <p className="inspector-problem" role="alert">
          {problem}
        </p>
      )}
    </>
  );
}

function splitRef(ref: string | undefined): [string, string] {
  if (!ref) return ["", ""];
  const at = ref.indexOf(":");
  return at < 0 ? ["", ""] : [ref.slice(0, at), ref.slice(at + 1)];
}

function EarlierTurns({ call, part }: { call: InspectedCall; part: PartRecord }) {
  const [open, setOpen] = useState(false);
  const turns = (call.context?.turns ?? []).map((t, i) => ({ t, preview: call.previews?.[i] ?? "" })).filter((x) => x.t.part === "recent");
  const summarised = turns.filter((x) => x.t.summarised).length;
  const words = [`${count(part.kept)} sent`];
  if (part.dropped > 0) words.push(`${count(part.dropped)} left out (${count(part.dropped_tokens)} tokens)`);
  if (summarised > 0) words.push(`${count(summarised)} in the summary`);
  if (turns.length === 0) return null;
  return (
    <>
      <p className="inspector-note">
        {words.join(", ")}.{" "}
        <button type="button" className="inspector-toggle" aria-expanded={open} onClick={() => setOpen((o) => !o)}>
          {open ? "Hide the turns" : "Show the turns"}
        </button>
      </p>
      {open && (
        <ol className="inspector-turns">
          {turns.map(({ t, preview }) => (
            <li key={t.first} className={t.kept ? "inspector-turn" : "inspector-turn is-out"}>
              <span className="inspector-turn-text">{preview || "A turn with no words"}</span>
              <span className="inspector-item-meta">
                {t.kept ? "sent" : t.summarised ? "in the summary" : "left out"} · {count(t.tokens)}
              </span>
            </li>
          ))}
        </ol>
      )}
    </>
  );
}

function SummaryDetail({ part, summaries }: { part: PartRecord; summaries: ChatSummary[] }) {
  const [open, setOpen] = useState(false);
  const id = part.ref?.startsWith("summary:") ? Number(part.ref.slice(8)) : 0;
  const sum = summaries.find((s) => s.id === id);
  const said = part.state === "kept" ? `Sent in place of ${part.covers === 1 ? "one earlier turn" : `${count(part.covers)} earlier turns`}.` : "Not sent: it did not fit its share of the call.";
  return (
    <>
      <p className="inspector-note">
        {said}{" "}
        {sum && (
          <button type="button" className="inspector-toggle" aria-expanded={open} onClick={() => setOpen((o) => !o)}>
            {open ? "Hide the summary" : "Read the summary"}
          </button>
        )}
      </p>
      {open && sum && (
        <div className="inspector-summary">
          <p className="inspector-summary-text">{sum.text}</p>
          <p className="inspector-item-meta">
            Written by {sum.model}
            {when(sum.created) && `, ${when(sum.created)}`}, for {sum.turns === 1 ? "one turn" : `${count(sum.turns)} turns`}
            {sum.sensitive && ". It holds sensitive content, so only local and trusted models are given it"}.
          </p>
        </div>
      )}
    </>
  );
}

function UsageLines({ records }: { records: UsageRecord[] }) {
  if (records.length === 0) return <p className="inspector-note">No usage was recorded: the call never ended.</p>;
  return (
    <ul className="inspector-usage">
      {records.map((r) => (
        <li key={r.id}>
          {r.model} · {outcomes[r.outcome] ?? r.outcome} · {count(r.input_tokens)} in · {count(r.output_tokens)} out
          {r.cache_read_tokens ? ` · ${count(r.cache_read_tokens)} cached` : ""} · {dollars(r.cost_micros)} · {seconds(r.duration_ms)}
          {r.estimated ? " · estimated" : ""}
        </li>
      ))}
    </ul>
  );
}

function RecallSection({ recall, removed }: { recall: RecallRecord; removed: Set<string> }) {
  const hits = recall.hits ?? [];
  const counts = [`${count(recall.found)} found`, `${count(recall.kept)} kept`];
  if (recall.withheld > 0) counts.push(`${count(recall.withheld)} withheld`);
  if (recall.dropped - recall.withheld > 0) counts.push(`${count(recall.dropped - recall.withheld)} dropped`);
  return (
    <section className="inspector-section" aria-label="Recall">
      <h3 className="inspector-heading">What recall found</h3>
      {recall.note ? (
        <p className="inspector-note">{recall.note}</p>
      ) : (
        <p className="inspector-note">
          {recall.query ? `Searched ${recall.adapter ? `the ${recall.adapter} store` : "the knowledge store"} for “${recall.query}”` : "Nothing was searched for"}: {counts.join(", ")}.
          {recall.sensitive && " A kept item is sensitive, so this chat goes only to local and trusted models."}
        </p>
      )}
      {hits.length > 0 && (
        <ul className="inspector-items">
          {hits.map((h) => (
            <li key={`${h.type}:${h.id}`} className={h.kept ? "inspector-item" : "inspector-item is-removed"}>
              <div className="inspector-item-text">
                <span className="inspector-item-label">
                  {h.space ? `${h.space}: ` : ""}
                  {h.title || `${h.type} ${h.id}`}
                </span>
                <span className="inspector-item-meta">
                  {h.type} {h.id} · score {h.score.toFixed(2)}
                  {h.sensitive ? " · sensitive" : ""} · {h.kept ? (removed.has(`${h.type}:${h.id}`) ? "kept, and left out by you" : "kept") : (reasons[h.reason ?? ""] ?? h.reason ?? "not kept")}
                </span>
              </div>
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}
