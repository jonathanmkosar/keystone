// The phone's sheets (decision 0590): the sheet behind the composer's + and
// the one behind the chat header's ... button. A sheet rises from the foot
// of what can be seen, over a scrim, and holds a full-width row for each
// control, with its icon, its name and its state. A control drawn inside a
// sheet asks useSheet() and draws itself as such a row, with its own picker
// opening in the sheet; it calls done() when a choice is made, which closes
// the sheet. Escape and a tap outside close it too.
//
// There is no mockup for either sheet: they are built from the tokens after
// the owner's example of another app's composer (November 27, 2026), and
// are provisional. The look is phone-sheet.css.

import { createContext, useContext, useEffect, useRef, useSyncExternalStore, type KeyboardEvent, type ReactNode, type Ref, type RefObject } from "react";
import { createPortal } from "react-dom";
import { rowLabel, phoneQuery } from "./composerSheet";
import { Icon, type IconName } from "./Icon";
import "./phone-sheet.css";

type SheetState = { done: () => void };

const SheetContext = createContext<SheetState | null>(null);

/** The sheet a control is drawn in, or null when it is drawn in its usual place. */
export function useSheet(): SheetState | null {
  return useContext(SheetContext);
}

function subscribe(fn: () => void): () => void {
  const m = window.matchMedia(phoneQuery);
  m.addEventListener("change", fn);
  return () => m.removeEventListener("change", fn);
}

/** The screen is a phone's width: the composer and the header are drawn for it. */
export function useNarrow(): boolean {
  return useSyncExternalStore(subscribe, () => window.matchMedia(phoneQuery).matches);
}

/** No box takes the focus by itself on a phone, or its keyboard would rise over the chat. */
export function mayTakeFocus(): boolean {
  return !window.matchMedia(`${phoneQuery}, (pointer: coarse)`).matches;
}

const focusable = 'button:not(:disabled), a[href], input:not(:disabled), [tabindex]';

/** The rows and boxes of a sheet the keyboard can reach, as drawn: not the file picker's hidden box. */
function reachable(box: HTMLElement | null): HTMLElement[] {
  return Array.from(box?.querySelectorAll<HTMLElement>(focusable) ?? []).filter((x) => x.tabIndex >= 0 && !x.closest(".offscreen") && x.getClientRects().length > 0);
}

/**
 * A sheet. It stays in the page while closed, hidden, so the controls in it
 * keep what they have read; it is drawn into the body, so it sits outside
 * the composer's form and the header.
 */
export function Sheet({ open, label, onClose, opener, children }: { open: boolean; label: string; onClose: () => void; opener: RefObject<HTMLElement | null>; children: ReactNode }) {
  const box = useRef<HTMLDivElement>(null);
  const was = useRef(false);

  // It rises from the foot of what can be seen. The on-screen keyboard
  // shrinks and moves that, and CSS cannot tell, so the sheet is told where
  // the visible part starts and how tall it is, as the attach picker is.
  useEffect(() => {
    const el = box.current;
    if (!open || !el) return;
    const vv = window.visualViewport;
    const place = () => {
      el.style.setProperty("--seen-top", `${vv ? vv.offsetTop : 0}px`);
      el.style.setProperty("--seen-height", `${vv ? vv.height : window.innerHeight}px`);
    };
    place();
    vv?.addEventListener("resize", place);
    vv?.addEventListener("scroll", place);
    return () => {
      vv?.removeEventListener("resize", place);
      vv?.removeEventListener("scroll", place);
    };
  }, [open]);

  // Escape closes it.
  useEffect(() => {
    if (!open) return;
    const key = (e: globalThis.KeyboardEvent) => {
      if (e.key === "Escape") onClose();
    };
    document.addEventListener("keydown", key);
    return () => document.removeEventListener("keydown", key);
  }, [open, onClose]);

  // Opened, the focus goes to its first row, a button, which raises no
  // keyboard; closed, back to the button that opened it.
  useEffect(() => {
    const el = box.current;
    if (open && !was.current) reachable(el)[0]?.focus();
    if (!open && was.current) {
      const at = document.activeElement;
      if (!at || at === document.body || el?.contains(at)) opener.current?.focus();
    }
    was.current = open;
  }, [open, opener]);

  // Arrow keys move between the rows that can be reached.
  function keyDown(e: KeyboardEvent<HTMLDivElement>) {
    if (e.key !== "ArrowDown" && e.key !== "ArrowUp") return;
    if ((e.target as HTMLElement).tagName === "INPUT") return;
    const items = reachable(box.current);
    if (items.length === 0) return;
    const at = items.indexOf(document.activeElement as HTMLElement);
    const next = e.key === "ArrowDown" ? (at + 1) % items.length : (at - 1 + items.length) % items.length;
    e.preventDefault();
    items[next].focus();
  }

  return createPortal(
    <div className="sheet-layer" hidden={!open}>
      <button type="button" className="sheet-scrim" aria-label="Close" tabIndex={-1} onClick={onClose} />
      <div className="sheet" role="dialog" aria-modal="true" aria-label={label} ref={box} onKeyDown={keyDown}>
        <div className="sheet-grip" aria-hidden="true" />
        <SheetContext.Provider value={{ done: onClose }}>{children}</SheetContext.Provider>
      </div>
    </div>,
    document.body,
  );
}

/**
 * One row of a sheet: its icon, its name and its state. With expanded it is
 * the head of the control's own picker, which opens under it in the sheet
 * while the other rows step aside (phone-sheet.css).
 */
export function SheetRow({
  icon,
  name,
  state = "",
  expanded,
  pressed,
  disabled = false,
  title,
  onClick,
  ref,
}: {
  icon: IconName;
  name: string;
  state?: string;
  /** The row opens a picker: true while it is open. */
  expanded?: boolean;
  /** The row is a switch: true while it is on. */
  pressed?: boolean;
  disabled?: boolean;
  title?: string;
  onClick: () => void;
  ref?: Ref<HTMLButtonElement>;
}) {
  return (
    <button
      type="button"
      ref={ref}
      className={["sheet-row", pressed ? "is-on" : "", expanded ? "is-open" : ""].filter(Boolean).join(" ")}
      aria-label={rowLabel(name, state)}
      aria-expanded={expanded}
      aria-haspopup={expanded === undefined ? undefined : "menu"}
      aria-pressed={pressed}
      disabled={disabled}
      title={title}
      onClick={onClick}
    >
      <Icon name={icon} size={20} />
      <span className="sheet-row-name">{name}</span>
      {state && <span className="sheet-row-state">{state}</span>}
      {expanded !== undefined && (
        <span className="sheet-row-caret">
          <Icon name={expanded ? "chevron" : "caret"} size={16} />
        </span>
      )}
    </button>
  );
}
