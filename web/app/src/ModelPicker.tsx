import { useCallback, useEffect, useRef, useState, type KeyboardEvent } from "react";
import { Link } from "react-router";
import { ApiError, chatModel, listModels, listSettings, type LadderStep, type ModelChoice, type ModelInfo, type RegisteredModel } from "./api";
import { autoFoot, autoLabel, autoLine, autoPick, ladderNames } from "./autoWords";
import { useChats } from "./chats";
import { footnote, shortModelName } from "./composerSheet";
import { Icon } from "./Icon";
import { Sheet, useNarrow } from "./PhoneSheet";
import { mayReadSensitive, newChatKey, pickModel, rememberChoice, shownModel, showsAuto, usePickEntry } from "./modelPicks";
import { isActive, useLive } from "./turns";
import "./model-picker.css";
import "./auto.css";

// The setting that names a person's model for a new chat. The new chat
// screen has no chat, so no model route: it works out the server's choice
// from the registry and this setting, in the server's order.
const defaultModelKey = "chats.default_model";
// The ladder and whether a new chat starts on Auto (P14-04), for the same.
const ladderKey = "router.ladder";
const autoDefaultKey = "router.auto_default";

type Row = {
  model: ModelInfo;
  current: boolean;
  keyMissing: boolean;
  /** Why it cannot be picked, for a person. Empty when it can. */
  blocked: string;
};

const keyMissingWhy = "Its key is not stored. Add it under Models and keys.";
const heldWhy = "Not for this chat: it holds content from a sensitive space, which only a local or trusted model may read.";

/**
 * The new chat's choice, as the server would make it: the person's
 * chats.default_model when it names a model a chat can use, or else the
 * first such model by name. The registry is listed by name.
 */
function newChatChoice(registry: RegisteredModel[], defaultName: string, ladderSetting = "", autoDefault = false): ModelChoice {
  const models: ModelInfo[] = registry
    // Embedding and decision models answer no chat (decision 0630).
    .filter((r) => !r.problem && !(r.entry.capabilities ?? []).some((c) => c === "embedding" || c === "decision"))
    .map((r) => ({ name: r.entry.name, label: r.entry.label, class: r.entry.class, test: r.entry.adapter === "fake" }));
  // The ladder as the server reads it: its names that are usable models, smallest first (P14-04).
  const ladder: LadderStep[] = [];
  ladderNames(ladderSetting).forEach((name, i) => {
    const m = models.find((x) => x.name === name);
    if (m) ladder.push({ name, label: m.label || name, class: m.class, step: i + 1 });
  });
  const auto = autoDefault && ladder.length > 0;
  const byDefault = models.find((m) => m.name === defaultName);
  if (byDefault) return { model: byDefault, source: "default", models, ladder, auto };
  if (models.length > 0) return { model: models[0], source: "first", models, ladder, auto };
  return { source: "none", models, ladder, auto };
}

function labelOf(m: ModelInfo): string {
  return m.label || m.name;
}

/** The sentence at the foot of the menu: where the model that will answer comes from. */
function footOf(choice: ModelChoice, pending: boolean, sent: boolean, isNew: boolean): string {
  if (choice.source === "test") return "The test tenant's replies are recorded test data, whatever model is picked.";
  if (pending) return isNew ? "The new chat starts with the model you picked." : "Sent with your next message. The chat keeps it after that.";
  if (sent) return "Sent with your message. The chat keeps it from now on.";
  switch (choice.source) {
    case "chat":
      return "The model this chat last used. It stays until you pick another.";
    case "default":
      return isNew ? "Your default model for new chats, set in General settings." : "Your default model, set in General settings.";
    case "first":
      return "The first model in your registry, since no default is set in General settings.";
    default:
      return "";
  }
}

/**
 * The model picker in the composer, beside the send button, as in
 * MainDark.html. For a chat it reads the chat's model route; for the new
 * chat, which has none yet, the registry and the default model setting.
 */
export function ModelPicker(props: { chatId: string; initial?: ModelChoice } | { newChatFor: string }) {
  const isNew = "newChatFor" in props;
  const key = isNew ? newChatKey(props.newChatFor) : props.chatId;
  const chatId = isNew ? "" : props.chatId;
  const initial = isNew ? undefined : props.initial;

  const { lost } = useChats();
  const entry = usePickEntry(key);
  const live = useLive(isNew ? undefined : chatId);
  const active = isActive(live);
  const narrow = useNarrow(); // a phone: the chip beside the composer's + shows the model's short name (decision 0590)
  const [registry, setRegistry] = useState<Map<string, RegisteredModel>>();
  const [open, setOpen] = useState(false);
  const wrap = useRef<HTMLDivElement>(null);
  const trigger = useRef<HTMLButtonElement>(null);
  const menu = useRef<HTMLDivElement>(null);

  function enabledItems(): HTMLButtonElement[] {
    return Array.from(menu.current?.querySelectorAll<HTMLButtonElement>('[role="menuitemradio"]:not(:disabled)') ?? []);
  }

  // Reads the choice and the registry again. idle says no turn of the chat
  // is under way, so the answer is the server's after the last one.
  const refresh = useCallback(
    async (idle: boolean) => {
      try {
        if (isNew) {
          const [models, settings] = await Promise.all([listModels(), listSettings()]);
          setRegistry(new Map(models.models.filter((r) => !r.problem).map((r) => [r.entry.name, r])));
          const value = (k: string) => settings.settings.find((s) => s.key === k)?.value ?? "";
          rememberChoice(key, newChatChoice(models.models, value(defaultModelKey), value(ladderKey), value(autoDefaultKey) === "true"), idle);
        } else {
          const [choice, models] = await Promise.all([chatModel(chatId), listModels().catch(() => undefined)]);
          if (models) setRegistry(new Map(models.models.filter((r) => !r.problem).map((r) => [r.entry.name, r])));
          rememberChoice(key, choice, idle);
        }
      } catch (e) {
        // What is on the screen stays. The send says what is wrong.
        if (e instanceof ApiError && e.kind === "signed-out") lost();
      }
    },
    [isNew, key, chatId, lost],
  );

  // Read on opening the chat, and again each time a turn of it ends: the
  // server may have taken a pick, or not.
  useEffect(() => {
    if (!active) void refresh(true);
  }, [active, refresh]);

  // A click outside, or Escape, closes the menu. On a phone the menu is a
  // sheet drawn into the body, which closes itself on a tap outside and on
  // Escape, so a tap inside it must not count as outside here.
  useEffect(() => {
    if (!open || narrow) return;
    function down(e: MouseEvent) {
      if (wrap.current && !wrap.current.contains(e.target as Node)) setOpen(false);
    }
    function escape(e: globalThis.KeyboardEvent) {
      if (e.key === "Escape") {
        setOpen(false);
        trigger.current?.focus();
      }
    }
    document.addEventListener("mousedown", down);
    document.addEventListener("keydown", escape);
    return () => {
      document.removeEventListener("mousedown", down);
      document.removeEventListener("keydown", escape);
    };
  }, [open, narrow]);

  // The menu opens on the model that will answer.
  useEffect(() => {
    if (!open) return;
    const items = enabledItems();
    (items.find((b) => b.getAttribute("aria-checked") === "true") ?? items[0])?.focus();
  }, [open]);

  const choice = entry?.choice ?? initial;
  if (!choice || choice.models.length === 0) return null;
  const pick = entry?.pick;
  const shown = shownModel(choice, pick);
  // The chat holds sensitive content: the model route says so (P14-04, decision 0696).
  const held = choice.held === true;
  // Auto is offered while the person has a ladder, and is what answers when picked or when the chat is on it (P14-04).
  const ladder = choice.ladder ?? [];
  const auto = showsAuto(choice, pick);

  const rows: Row[] = choice.models.map((m) => {
    const reg = registry?.get(m.name);
    const keyMissing = reg !== undefined && !reg.key_present;
    let blocked = "";
    if (keyMissing) blocked = keyMissingWhy;
    else if (held && !mayReadSensitive(m)) blocked = heldWhy;
    return { model: m, current: !auto && m.name === shown?.name, keyMissing, blocked };
  });
  const shownRow = rows.find((r) => r.current);
  const pending = pick !== undefined && !pick.sent;
  const foot =
    autoFoot({ auto: choice.auto === true, pickedAuto: pick?.name === autoPick, pickedModel: pick !== undefined && pick.name !== autoPick, pending, isNew, lastStep: ladder.find((s) => s.name === choice.last_step)?.label }) ||
    footOf(choice, pending, pick?.sent === true, isNew);

  function toggle() {
    if (!open) void refresh(!active);
    setOpen((o) => !o);
  }

  function choose(row: Row) {
    if (row.blocked) return;
    pickModel(key, row.model.name);
    setOpen(false);
    trigger.current?.focus();
  }

  function chooseAuto() {
    pickModel(key, autoPick);
    setOpen(false);
    trigger.current?.focus();
  }

  function menuKey(e: KeyboardEvent<HTMLDivElement>) {
    const items = enabledItems();
    if (items.length === 0) return;
    const at = items.indexOf(document.activeElement as HTMLButtonElement);
    let next = -1;
    if (e.key === "ArrowDown") next = (at + 1) % items.length;
    else if (e.key === "ArrowUp") next = (at - 1 + items.length) % items.length;
    else if (e.key === "Home") next = 0;
    else if (e.key === "End") next = items.length - 1;
    else if (e.key === "Tab") setOpen(false);
    if (next >= 0) {
      e.preventDefault();
      items[next].focus();
    }
  }

  const label = auto ? autoLabel : shown ? labelOf(shown) : "Pick a model";
  const warn = auto ? "" : (shownRow?.blocked ?? "");
  const autoRow = ladder.length > 0 ? { current: auto, steps: ladder.length, choose: chooseAuto } : undefined;
  const menuBody = <MenuBody isNew={isNew} rows={rows} held={held} foot={foot} choose={choose} close={() => setOpen(false)} phone={narrow} auto={autoRow} />;

  return (
    <div className="model-picker" ref={wrap}>
      <button
        type="button"
        ref={trigger}
        className={warn ? "model-button is-blocked" : "model-button"}
        aria-haspopup="menu"
        aria-expanded={open}
        aria-label={`Model: ${label}${warn ? `. ${warn}` : ""}. Pick another model`}
        title={warn || `The model that will answer: ${label}`}
        onClick={toggle}
      >
        {warn && <Icon name="alert" size={16} />}
        <span className="model-button-label">{narrow ? shortModelName(label) : label}</span>
        <Icon name="chevron" size={16} />
      </button>
      {narrow ? (
        // On a phone the menu rises from the foot of the screen as a sheet,
        // as the composer's + does: opening upward from the chip, it ran off
        // the top of the new chat screen (the owner's report, November 27, 2026).
        <Sheet open={open} label={isNew ? "Model for the new chat" : "Model for this chat"} onClose={() => setOpen(false)} opener={trigger}>
          {open && <div className="model-sheet" role="menu" aria-label={isNew ? "Model for the new chat" : "Model for this chat"} ref={menu} onKeyDown={menuKey}>{menuBody}</div>}
        </Sheet>
      ) : open && (
        <div className="model-menu" role="menu" aria-label={isNew ? "Model for the new chat" : "Model for this chat"} ref={menu} onKeyDown={menuKey}>
          {menuBody}
        </div>
      )}
    </div>
  );
}

type AutoRow = { current: boolean; steps: number; choose: () => void };

function MenuBody({ isNew, rows, held, foot, choose, close, phone, auto }: { isNew: boolean; rows: Row[]; held: boolean; foot: string; choose: (r: Row) => void; close: () => void; phone: boolean; auto?: AutoRow }) {
  return (
    <>
          <p className="model-menu-head">{isNew ? "Model for the new chat" : "Model for this chat"}</p>
          {/* Auto first, while the person has a ladder (P14-04), on a computer and on a phone's sheet alike. */}
          {auto && (
            <button type="button" role="menuitemradio" aria-checked={auto.current} className={auto.current ? "model-item is-auto is-current" : "model-item is-auto"} onClick={auto.choose}>
              <span className="model-item-check">{auto.current && <Icon name="check" size={16} />}</span>
              <span className="model-item-text">
                <span className="model-item-label">{autoLabel}</span>
                <span className="model-item-why">{autoLine(auto.steps)}</span>
              </span>
              <span className="model-tags">
                <span className="model-tag is-auto">auto</span>
              </span>
            </button>
          )}
          {rows.map((r) => (
            <button
              key={r.model.name}
              type="button"
              role="menuitemradio"
              aria-checked={r.current}
              className={r.current ? "model-item is-current" : "model-item"}
              disabled={r.blocked !== ""}
              onClick={() => choose(r)}
            >
              <span className="model-item-check">{r.current && <Icon name="check" size={16} />}</span>
              <span className="model-item-text">
                <span className="model-item-label">{labelOf(r.model)}</span>
                {r.blocked && <span className="model-item-why">{r.blocked}</span>}
              </span>
              <span className="model-tags">
                <span className={mayReadSensitive(r.model) ? "model-tag is-private" : "model-tag"}>{r.model.class}</span>
                {r.keyMissing && <span className="model-tag is-bad">key missing</span>}
              </span>
            </button>
          ))}
          {held && <p className="model-menu-note">This chat holds content from a sensitive space, so only local and trusted models can answer it.</p>}
          {foot && <p className="model-menu-note">{foot}</p>}
          <Link className="model-menu-link" to="/settings/models" onClick={close}>
            Models and keys
          </Link>
          {/* On a phone the note under the composer is gone; its words are here (P10-07). */}
          {phone && <p className="model-menu-note model-menu-foot">{footnote}</p>}
    </>
  );
}
