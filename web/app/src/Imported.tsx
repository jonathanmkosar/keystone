// Imported chats (P12-03), provisional on the mockups, which draw no
// imported chat: the small mark such a chat wears on its row, and the line
// at the top of its screen. A chat brought in from New Keystone is an
// ordinary chat here; the mark says where it came from and what New
// Keystone recorded that the chat record cannot hold. The server sends the
// mark with the chat (docs/architecture/import.md).

import type { Chat, Imported } from "./api";
import { useChats } from "./chats";
import "./imported.css";

const day = new Intl.DateTimeFormat(undefined, { day: "numeric", month: "short", year: "numeric" });

function dayOf(iso: string): string {
  const d = new Date(iso);
  return Number.isNaN(d.getTime()) ? "" : day.format(d);
}

function count(n: number, one: string, many: string): string {
  return n === 1 ? `1 ${one}` : `${n} ${many}`;
}

/** What the chat was where it came from, when it was said, and by which models. */
export function importedFacts(m: Imported): string {
  const what = m.kind === "routine" ? "A routine's run" : m.kind === "build" ? "A build" : count(m.turns, "turn", "turns");
  const first = dayOf(m.first);
  const last = dayOf(m.last);
  const when = first && last ? (first === last ? ` on ${first}` : `, ${first} to ${last}`) : "";
  const models = m.models.length > 0 ? ` Answered there by ${m.models.join(", ")}.` : "";
  const silent = m.unanswered ? ` ${count(m.unanswered, "message", "messages")} got no reply there.` : "";
  return `${what}${when}.${models}${silent}`;
}

/** The small mark on an imported chat's row. */
export function ImportedTag({ chat }: { chat: Chat }) {
  if (!chat.imported) return null;
  return (
    <span className="imported-tag" title={`Imported from ${chat.imported.from}. ${importedFacts(chat.imported)}`}>
      imported
    </span>
  );
}

/** The line at the top of an imported chat's screen. */
export function ImportedNote({ chatId }: { chatId: string }) {
  const { chats } = useChats();
  const mark = chats.find((c) => c.id === chatId)?.imported;
  if (!mark) return null;
  return (
    <div className="imported-note" role="note">
      <p>
        <strong>Imported from {mark.from}.</strong> {importedFacts(mark)}
      </p>
      {mark.note && <p className="imported-note-more">{mark.note}</p>}
    </div>
  );
}
