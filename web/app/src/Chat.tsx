import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import { Link, useNavigate, useParams } from "react-router";
import { ApiError, createChat, openChat, type Message, type ModelChoice, type Person } from "./api";
import { Approvals } from "./Approval";
import { useChats } from "./chats";
import { Composer } from "./Composer";
import { Conversation } from "./Conversation";
import { Icon } from "./Icon";
import { Inspector } from "./Inspector";
import { layout } from "./layout";
import { Mark } from "./Mark";
import { ModelPicker } from "./ModelPicker";
import { adoptNewChatPick, takePick, useShownChoice } from "./modelPicks";
import { isActive, join, send, settle, stop, useLive } from "./turns";

const footnote = "Keystone can make mistakes. Check what matters against its sources.";

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
  const part = partOfDay();

  async function begin(text: string) {
    setBusy(true);
    setProblem("");
    try {
      const made = await createChat();
      put(made.chat, true);
      send(made.chat.id, text, adoptNewChatPick(person.id, made.chat.id));
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
    <div className="start">
      <div className="start-greeting">
        <Mark size={40} />
        <h1 className="home-greeting">
          Good {part}, {person.name}
        </h1>
      </div>
      <div className="composer-column">
        <Composer key={unsent.tries} large placeholder={`How can I help you this ${part}?`} busy={busy} focusKey="start" initial={unsent.text} onSend={begin} beside={<ModelPicker newChatFor={person.id} />} />
        {problem && (
          <p className="composer-problem" role="alert">
            {problem}
          </p>
        )}
      </div>
    </div>
  );
}

type Loaded =
  | { state: "loading" }
  | { state: "ready"; messages: Message[]; model: ModelChoice }
  | { state: "missing" }
  | { state: "failed" };

function modelLine(model: ModelChoice): string {
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

  function sendHere(text: string) {
    stick.current = true;
    send(id, text, takePick(id));
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
  const noModel = loaded.state === "ready" && !loaded.model.model;
  const note = loaded.state === "ready" && shown ? modelLine(shown) : "";

  return (
    <div className="chat">
      <div className="chat-scroll" ref={scroller} onScroll={scrolled}>
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
      </div>
      <div className="composer-dock">
        <div className="composer-column">
          {noModel && (
            <p className="composer-problem" role="alert">
              {(loaded.state === "ready" && loaded.model.reason) || "There is no model to answer in this chat."}
            </p>
          )}
          <Composer
            placeholder="Reply to Keystone…"
            busy={busy || loaded.state === "loading"}
            stopping={live?.stopping ?? false}
            focusKey={id}
            onSend={sendHere}
            onStop={busy ? () => stop(id) : undefined}
            beside={<ModelPicker chatId={id} initial={loaded.state === "ready" ? loaded.model : undefined} />}
          />
          <p className="composer-note">{note ? `${note} · ${footnote}` : footnote}</p>
        </div>
      </div>
      <Inspector chatId={id} />
    </div>
  );
}
