import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import { Link, useNavigate, useParams } from "react-router";
import { ApiError, createChat, openChat, type Message, type ModelChoice, type Person } from "./api";
import { Approvals } from "./Approval";
import { ArtifactPanel } from "./ArtifactPanel";
import { AttachChatRow, AttachedChats } from "./AttachedChats";
import { useChats } from "./chats";
import { Composer } from "./Composer";
import { applyNewChatConnectors, ConnectorMenu } from "./ConnectorMenu";
import { Conversation } from "./Conversation";
import { Icon } from "./Icon";
import { ImportedNote } from "./Imported";
import { Inspector } from "./Inspector";
import { IncognitoMark, IncognitoSwitch, setNewChatIncognito, useNewChatIncognito } from "./Incognito";
import { footnote, phoneGreeting } from "./composerSheet";
import { useNarrow } from "./PhoneSheet";
import { createIncognitoChat } from "./memoryCalls";
import { layout } from "./layout";
import { Mark } from "./Mark";
import { OperationsNotice } from "./OperationsNotice";
import { ResearchCards, ResearchControl, ResearchHint } from "./Research";
import { newChatKey as researchKey, sendAsResearch } from "./researchCalls";
import { Tasks } from "./Tasks";
import { ModelPicker } from "./ModelPicker";
import { adoptNewChatPick, takePick, useShownChoice } from "./modelPicks";
import { applyNewChatStyle, StylePicker } from "./StylePicker";
import { isActive, join, send, settle, stop, useLive } from "./turns";
import { messageWith } from "./uploads";
import type { SentPosition } from "./location";

function partOfDay(): "morning" | "afternoon" | "evening" {
  const hour = new Date().getHours();
  if (hour < 12) return "morning";
  if (hour < 18) return "afternoon";
  return "evening";
}

/** New chat: the greeting over a composer. The first message makes the chat. */
export function Start({ person }: { person: Person }) {
  const navigate = useNavigate();
  const { put, lost } = useChats();
  const [busy, setBusy] = useState(false);
  const [problem, setProblem] = useState("");
  // A message that could not be sent goes back into the box.
  const [unsent, setUnsent] = useState({ text: "", tries: 0 });
  // P6-07: a chat that keeps no record. Its switch is under the composer, or
  // on a phone a round button in the header (P10-07), so it is held outside
  // this screen; leaving the screen turns it off, as it always has.
  const incognito = useNewChatIncognito();
  useEffect(() => () => setNewChatIncognito(false), []);
  const narrow = useNarrow(); // a phone: the greeting in the free space, the composer at the foot (P10-07)
  const part = partOfDay();

  async function begin(text: string, files: File[], location?: SentPosition) {
    setBusy(true);
    setProblem("");
    try {
      const made = incognito ? await createIncognitoChat() : await createChat();
      put(made.chat, true);
      await applyNewChatConnectors(person.id, made.chat.id); // the tools menu's switches (P7-04)
      await applyNewChatStyle(person.id, made.chat.id); // the style picker's pick (P9-05)
      // The Research control was on (P8-04): the message is a research question.
      if (sendAsResearch(researchKey(person.id), made.chat.id, text)) {
        navigate(`/chats/${made.chat.id}`);
        return;
      }
      send(made.chat.id, text, adoptNewChatPick(person.id, made.chat.id), files.length > 0 ? () => messageWith(made.chat.id, text, files) : undefined, location);
      navigate(`/chats/${made.chat.id}`);
    } catch (e) {
      setBusy(false);
      setUnsent((u) => ({ text, tries: u.tries + 1 }));
      if (e instanceof ApiError && e.kind === "signed-out") lost();
      else if (e instanceof ApiError && (e.code === "not_built" || e.code === "no_route")) setProblem("This server cannot start a chat yet: that part of it is not built.");
      else setProblem("The chat could not be started. Check that the server is running, then send the message again.");
    }
  }

  return (
    <div className={narrow ? "start is-phone" : "start"}>
      <div className="start-greeting">
        <Mark size={narrow ? 34 : 40} />
        <h1 className="home-greeting">{narrow ? phoneGreeting(part, person.name) : `Good ${part}, ${person.name}`}</h1>
        {narrow && incognito && (
          <p className="start-incognito" role="status">
            Incognito: nothing from this chat is remembered, and it is deleted when you leave it.
          </p>
        )}
      </div>
      {narrow && <OperationsNotice />}
      <div className="composer-column">
        <ResearchHint newChatFor={person.id} />
        <Composer key={unsent.tries} large placeholder={`How can I help you this ${part}?`} busy={busy} focusKey="start" initial={unsent.text} onSend={begin} beside={<><ResearchControl newChatFor={person.id} /><ConnectorMenu newChatFor={person.id} /><StylePicker newChatFor={person.id} /></>} model={<ModelPicker newChatFor={person.id} />} />
        {!narrow && <IncognitoSwitch on={incognito} onChange={setNewChatIncognito} />}
        {problem && (
          <p className="composer-problem" role="alert">
            {problem}
          </p>
        )}
      </div>
      {!narrow && <OperationsNotice />}
    </div>
  );
}

type Loaded =
  | { state: "loading" }
  | { state: "ready"; messages: Message[]; model: ModelChoice }
  | { state: "missing" }
  | { state: "failed" };

function modelLine(model: ModelChoice): string {
  if (model.auto) return "Auto"; // the router picks each message's model (P14-04)
  if (!model.model) return "";
  return model.model.test ? `${model.model.label || model.model.name} · recorded test replies` : model.model.label || model.model.name;
}

/** The chat at this address. Each chat gets a screen of its own, so nothing of the last one shows. */
export function ChatPage() {
  const { id = "" } = useParams();
  return <OneChat key={id} id={id} />;
}

/** One chat: the conversation on its current branch, and the composer under it. */
function OneChat({ id }: { id: string }) {
  const { put, lost } = useChats();
  const live = useLive(id);
  const [loaded, setLoaded] = useState<Loaded>({ state: "loading" });
  const shown = useShownChoice(id, loaded.state === "ready" ? loaded.model : undefined);
  const scroller = useRef<HTMLDivElement>(null);
  const stick = useRef(true);
  const narrow = useNarrow(); // a phone: no note under the composer (P10-07)

  // The phone's composer grows when it takes the focus and shrinks when it
  // leaves (P10-07): the end of the chat stays in view if it was.
  useEffect(() => {
    const el = scroller.current;
    if (!el || typeof ResizeObserver === "undefined") return;
    const watch = new ResizeObserver(() => {
      if (stick.current) el.scrollTop = el.scrollHeight;
    });
    watch.observe(el);
    return () => watch.disconnect();
  }, [loaded.state]);

  const load = useCallback(
    async (quiet: boolean) => {
      if (!quiet) setLoaded({ state: "loading" });
      try {
        const opened = await openChat(id);
        put(opened.chat);
        setLoaded({ state: "ready", messages: opened.messages, model: opened.model });
        // The record now holds what an ended turn wrote, so the turn can go.
        settle(id);
        // A turn that has not ended, sent before this page was opened or
        // from another window: join its stream.
        const waiting = opened.turns.at(0);
        if (waiting) join(id, waiting.id);
      } catch (e) {
        if (e instanceof ApiError && e.kind === "signed-out") lost();
        else if (quiet) return; // what is on the screen stays
        else setLoaded({ state: e instanceof ApiError && e.kind === "missing" ? "missing" : "failed" });
      }
    },
    [id, put, lost],
  );

  useEffect(() => {
    stick.current = true;
    void load(false);
  }, [load]);

  // When the turn ends, read the chat again: the record is the truth, and
  // it says whether another turn is waiting.
  const ended = live !== undefined && !isActive(live) && !live.settled;
  const ready = loaded.state === "ready";
  useEffect(() => {
    if (ended && ready) void load(true);
  }, [ended, ready, load]);

  const messages = loaded.state === "ready" ? loaded.messages : undefined;
  const items = useMemo(() => (messages ? layout(messages, live) : []), [messages, live]);

  // Follow the reply down the page, unless the person has scrolled up to read.
  useLayoutEffect(() => {
    const el = scroller.current;
    if (el && stick.current) el.scrollTop = el.scrollHeight;
  }, [items]);

  function scrolled() {
    const el = scroller.current;
    if (el) stick.current = el.scrollHeight - el.scrollTop - el.clientHeight < 80;
  }

  function sendHere(text: string, files: File[], location?: SentPosition) {
    stick.current = true;
    if (sendAsResearch(id, id, text)) return; // the Research control was on (P8-04): the message is a research question
    send(id, text, takePick(id), files.length > 0 ? () => messageWith(id, text, files) : undefined, location);
  }

  if (loaded.state === "missing" || loaded.state === "failed") {
    return (
      <div className="chat">
        <div className="chat-scroll">
          <div className="chat-state">
            <h1 className="page-title">{loaded.state === "missing" ? "This chat is not here" : "The chat could not be opened"}</h1>
            <p className="quiet">
              {loaded.state === "missing"
                ? "There is no chat at this address in your record. It may have been deleted."
                : "Keystone did not answer. Check that the server is running, then try again."}
            </p>
            {loaded.state === "missing" ? (
              <Link to="/" className="button">
                Start a new chat
              </Link>
            ) : (
              <button type="button" className="button" onClick={() => void load(false)}>
                <Icon name="retry" size={16} />
                Try again
              </button>
            )}
          </div>
        </div>
      </div>
    );
  }

  const busy = isActive(live);
  // A chat on Auto needs no model picked by hand (P14-05: the admin may allow none).
  const noModel = loaded.state === "ready" && !loaded.model.model && shown?.auto !== true;
  const note = loaded.state === "ready" && shown ? modelLine(shown) : "";

  return (
    <div className="chat">
      <div className="chat-scroll" ref={scroller} onScroll={scrolled}>
        <IncognitoMark chatId={id} />
        <ImportedNote chatId={id} />
        {loaded.state === "loading" ? (
          <div className="chat-state" aria-busy="true">
            <p className="quiet">Opening the chat.</p>
          </div>
        ) : items.length === 0 ? (
          <div className="chat-state">
            <p className="quiet">Nothing has been said in this chat yet.</p>
          </div>
        ) : (
          <Conversation items={items} />
        )}
        <Approvals chatId={id} />
        <Tasks chatId={id} onEnded={() => void load(true)} />
        <ResearchCards chatId={id} onEnded={() => void load(true)} />
      </div>
      <div className="composer-dock">
        <div className="composer-column">
          {noModel && (
            <p className="composer-problem" role="alert">
              {(loaded.state === "ready" && loaded.model.reason) || "There is no model to answer in this chat."}
            </p>
          )}
          <AttachedChats chatId={id} />
          <ResearchHint chatId={id} />
          <Composer
            placeholder="Reply to Keystone…"
            busy={busy || loaded.state === "loading"}
            stopping={live?.stopping ?? false}
            focusKey={id}
            onSend={sendHere}
            onStop={busy ? () => stop(id) : undefined}
            beside={<><AttachChatRow chatId={id} /><ResearchControl chatId={id} /><ConnectorMenu chatId={id} /><StylePicker chatId={id} /></>}
            model={<ModelPicker chatId={id} initial={loaded.state === "ready" ? loaded.model : undefined} />}
          />
          {/* A phone has no room for the note: the footnote is at the foot of
              the model's sheet and the model is on its chip. The test
              tenant's notice stays on screen (P10-07). */}
          {!narrow ? <p className="composer-note">{note ? `${note} · ${footnote}` : footnote}</p> : shown?.model?.test ? <p className="composer-note is-test">Test tenant: recorded test replies</p> : null}
        </div>
      </div>
      <Inspector chatId={id} />
      <ArtifactPanel chatId={id} />
    </div>
  );
}
