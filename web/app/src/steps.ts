// What a tool step says on the screen: one readable line from the tool's
// name and what it was sent, in the present while it runs and in the past
// once it has answered, and a few words on the right about what came back.
// The server sends only the name, the input and the result (decision 0204).
//
// A tool this file does not know is still shown: "Used <name>", with the
// first few values it was sent.

import type { IconName } from "./Icon";
import { sourcesOfText } from "./citations";

export type ToolLine = {
  icon: IconName;
  /** What it is doing, while it runs. */
  running: string;
  /** What the model asked for, for a step that was not run or gave no result. */
  asked: string;
  /** What it did, once it has answered. */
  done: string;
  /** A few words on what came back, shown on the right. Empty for none. */
  meta: string;
};

type Args = Record<string, unknown>;

function parse(input: string): Args {
  try {
    const v: unknown = JSON.parse(input || "{}");
    return v && typeof v === "object" && !Array.isArray(v) ? (v as Args) : {};
  } catch {
    return {};
  }
}

function cut(text: string, most: number): string {
  const one = text.replace(/\s+/g, " ").trim();
  return one.length > most ? one.slice(0, most - 1).trimEnd() + "…" : one;
}

function quoted(v: unknown, most = 48): string {
  return typeof v === "string" && v.trim() !== "" ? `“${cut(v, most)}”` : "";
}

/** The input as a short line: its plain values, in order, cut short. */
export function inputSummary(input: string, most = 64): string {
  const parts: string[] = [];
  for (const [key, v] of Object.entries(parse(input))) {
    if (typeof v === "string" && v.trim() !== "") parts.push(`${key.replace(/_/g, " ")} ${quoted(v, 32)}`);
    else if (typeof v === "number" || typeof v === "boolean") parts.push(`${key.replace(/_/g, " ")} ${String(v)}`);
  }
  return cut(parts.join(", "), most);
}

// The words a result opens with, for the tools that come with Keystone. They
// are the server's, in internal/tools/builtin.
function matches(result: string | undefined): string {
  if (result === undefined) return "";
  const found = /^(\d+) match(es)? for /.exec(result);
  if (found) return found[1] === "1" ? "1 match" : `${found[1]} matches`;
  return /^No other chat matches/.test(result) ? "none found" : "";
}

function chatTitle(result: string | undefined): string {
  const m = result === undefined ? null : /^Chat \S+: "((?:[^"\\]|\\.)*)"/.exec(result);
  if (!m) return "";
  try {
    return JSON.parse(`"${m[1]}"`) as string;
  } catch {
    return m[1];
  }
}

const known: Record<string, (args: Args, result: string | undefined) => ToolLine> = {
  current_time: () => ({ icon: "scheduled", running: "Checking the date and time", asked: "Check the date and time", done: "Checked the date and time", meta: "" }),
  search_chats: (args, result) => {
    const q = quoted(args.query);
    return {
      icon: "search",
      running: q ? `Searching your chats for ${q}` : "Searching your chats",
      asked: q ? `Search your chats for ${q}` : "Search your chats",
      done: q ? `Searched your chats for ${q}` : "Searched your chats",
      meta: matches(result),
    };
  },
  read_chat: (_args, result) => {
    const title = chatTitle(result);
    return { icon: "chats", running: "Reading one of your chats", asked: "Read one of your chats", done: title ? `Read ${quoted(title, 56)}` : "Read one of your chats", meta: "" };
  },
  // Background tasks (P8-01): the task's title, or the first words of its task.
  start_background_task: (args) => {
    const t = quoted(args.title) || quoted(args.task);
    return {
      icon: "agents",
      running: t ? `Starting a background task, ${t}` : "Starting a background task",
      asked: t ? `Start a background task, ${t}` : "Start a background task",
      done: t ? `Started a background task, ${t}` : "Started a background task",
      meta: "",
    };
  },
  // The connectors' (P7-04): a connector's own tools read as "Used notes_search: …".
  load_tools: (args) => {
    const names = Array.isArray(args.names) ? args.names.filter((n): n is string => typeof n === "string") : [];
    const which = names.length === 0 ? "connector tools" : cut(names.join(", "), 64);
    return { icon: "plug", running: `Loading ${which}`, asked: `Load ${which}`, done: `Loaded ${which}`, meta: names.length > 1 ? `${names.length} tools` : "" };
  },
  // Gmail and Calendar (P12-02): their tools, read as what they do to the person's mail and calendar.
  gmail_search: (args) => {
    const q = quoted(args.query);
    return { icon: "mail", running: q ? `Searching your mail for ${q}` : "Reading your newest mail", asked: q ? `Search your mail for ${q}` : "Read your newest mail", done: q ? `Searched your mail for ${q}` : "Read your newest mail", meta: "" };
  },
  gmail_read: () => ({ icon: "mail", running: "Reading an email", asked: "Read an email", done: "Read an email", meta: "" }),
  gmail_draft: (args) => {
    const to = recipients(args.to);
    return { icon: "mail", running: `Drafting an email${to}`, asked: `Draft an email${to}`, done: `Drafted an email${to}`, meta: "not sent" };
  },
  gmail_send: (args) => {
    const to = recipients(args.to);
    return { icon: "mail", running: `Sending an email${to}`, asked: `Send an email${to}`, done: `Sent an email${to}`, meta: "" };
  },
  calendar_list_events: () => ({ icon: "calendar", running: "Reading your calendar", asked: "Read your calendar", done: "Read your calendar", meta: "" }),
  calendar_get_event: () => ({ icon: "calendar", running: "Reading an event", asked: "Read an event", done: "Read an event", meta: "" }),
  calendar_create_event: (args) => {
    const t = quoted(args.title);
    return { icon: "calendar", running: `Adding ${t || "an event"} to your calendar`, asked: `Add ${t || "an event"} to your calendar`, done: `Added ${t || "an event"} to your calendar`, meta: guestCount(args.guests) };
  },
  calendar_update_event: (args) => {
    const t = quoted(args.title) || "an event";
    return { icon: "calendar", running: `Changing ${t}`, asked: `Change ${t}`, done: `Changed ${t}`, meta: guestCount(args.guests) };
  },
  calendar_delete_event: (args) => {
    const t = quoted(args.title) || "an event";
    return { icon: "calendar", running: `Deleting ${t}`, asked: `Delete ${t}`, done: `Deleted ${t}`, meta: guestCount(args.guests) };
  },
  // The web tools (P7-03). Their results are numbered sources: citations.tsx.
  web_search: (args, result) => {
    const q = quoted(args.query);
    const found = result === undefined ? -1 : sourcesOfText(result).length;
    return {
      icon: "web",
      running: q ? `Searching the web for ${q}` : "Searching the web",
      asked: q ? `Search the web for ${q}` : "Search the web",
      done: q ? `Searched the web for ${q}` : "Searched the web",
      meta: found < 0 ? "" : found === 0 ? "nothing found" : found === 1 ? "1 result" : `${found} results`,
    };
  },
  web_fetch: (args, result) => {
    const site = siteOf(args.url);
    const title = result === undefined ? "" : (sourcesOfText(result)[0]?.title ?? "");
    const named = title !== "" && !/^https?:\/\//.test(title);
    return {
      icon: "web",
      running: site ? `Reading ${site}` : "Reading a web page",
      asked: site ? `Read ${site}` : "Read a web page",
      done: named ? `Read ${quoted(title, 56)}` : site ? `Read ${site}` : "Read a web page",
      meta: named ? site : "",
    };
  },
};

/** " to a, b" for an email's recipients (P12-02), cut short; empty for none. */
function recipients(v: unknown): string {
  const list = Array.isArray(v) ? v.filter((x): x is string => typeof x === "string" && x.trim() !== "") : [];
  return list.length === 0 ? "" : ` to ${cut(list.join(", "), 56)}`;
}

/** How many guests an event's call names (P12-02), for the right of its row. */
function guestCount(v: unknown): string {
  const n = Array.isArray(v) ? v.filter((x) => typeof x === "string" && x.trim() !== "").length : 0;
  return n === 0 ? "" : n === 1 ? "1 guest" : `${n} guests`;
}

/** The site of an address, for a row: its host without "www.". */
function siteOf(v: unknown): string {
  if (typeof v !== "string" || v.trim() === "") return "";
  try {
    return new URL(v.includes("://") ? v : "https://" + v).host.replace(/^www\./, "");
  } catch {
    return cut(v, 40);
  }
}

/** The line for one tool step. `result` is undefined until the tool has answered. */
export function toolLine(name: string, input: string, result: string | undefined): ToolLine {
  const describe = known[name];
  if (describe) return describe(parse(input), result);
  const what = inputSummary(input);
  const tail = what ? `: ${what}` : "";
  return { icon: "tool", running: `Using ${name}${tail}`, asked: `Use ${name}${tail}`, done: `Used ${name}${tail}`, meta: "" };
}

/** The title of an approval card (P7-02): what the model asks to do, for a tool this file knows, or "Run <name>". */
export function toolTitle(name: string, input: string): string {
  const describe = known[name];
  return describe ? describe(parse(input), undefined).asked : `Run ${name}`;
}

/**
 * How a step that came back as an error is labelled. The record keeps only
 * the result's words, so the server's own sentences are read: a tool that
 * was not run (it needs an approval, the turn was at its step limit, or
 * tools are off) and a tool that was stopped are told from one that failed.
 * The words are in internal/tools and internal/turn/tools.go. `broke` is
 * true for a tool that failed, which is drawn in the error colour; a tool
 * Keystone chose not to run is not an error of the tool's.
 */
export function errorLabel(result: string | undefined): { text: string; broke: boolean } {
  const text = result ?? "";
  if (/turn was stopped/i.test(text)) return { text: "stopped", broke: false };
  if (/\bdeclined\b/i.test(text)) return { text: "declined", broke: false }; // the person said no (P7-02)
  if (/\bwas not run\b|\bno tool was run\b/i.test(text)) return { text: "not run", broke: false };
  if (/^There is no tool named/.test(text)) return { text: "no such tool", broke: true };
  // The web tools' own words, in internal/tools/web: no key yet, or a chat held for sensitive content.
  if (/^Web search is not set up/.test(text)) return { text: "not set up", broke: false };
  if (/is off in this chat/.test(text)) return { text: "off in this chat", broke: false };
  return { text: "failed", broke: true };
}
