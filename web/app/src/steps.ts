// What a tool step says on the screen: one readable line from the tool's
// name and what it was sent, in the present while it runs and in the past
// once it has answered, and a few words on the right about what came back.
// The server sends only the name, the input and the result (decision 0204).
//
// A tool this file does not know is still shown: "Used <name>", with the
// first few values it was sent.

import type { IconName } from "./Icon";

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
};

/** The line for one tool step. `result` is undefined until the tool has answered. */
export function toolLine(name: string, input: string, result: string | undefined): ToolLine {
  const describe = known[name];
  if (describe) return describe(parse(input), result);
  const what = inputSummary(input);
  const tail = what ? `: ${what}` : "";
  return { icon: "tool", running: `Using ${name}${tail}`, asked: `Use ${name}${tail}`, done: `Used ${name}${tail}`, meta: "" };
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
  if (/\bwas not run\b|\bno tool was run\b/i.test(text)) return { text: "not run", broke: false };
  if (/^There is no tool named/.test(text)) return { text: "no such tool", broke: true };
  return { text: "failed", broke: true };
}
