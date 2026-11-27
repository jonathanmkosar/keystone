// What the General settings screen works out from the settings route
// before it draws anything (P11-04): each setting's name and short line, the
// heading it goes under, what a search finds, and how its value reads.
// docs/architecture/settings-screens.md describes it.
//
// This file imports nothing, so Node's own test runner runs it as it is
// (decision 0501), against the real settings/*.json:
// web/app/test/settingsView.test.ts.

/** The fields of a setting this file reads. The route's Setting has them all. */
export type Entry = {
  key: string;
  title?: string;
  summary?: string;
  description: string;
  type: string;
  default: string;
  options: string[];
  value: string;
  overridden: boolean;
  changeable: boolean;
};

/**
 * The setting's readable name: the registry's title. A setting added with
 * none still has a name, made from its key, as every setting had before:
 * "chats.search_max_results" reads as "Search max results".
 */
export function titleOf(s: Pick<Entry, "key" | "title">): string {
  if (s.title && s.title.trim() !== "") return s.title;
  const words = s.key.slice(s.key.indexOf(".") + 1).replace(/[._]/g, " ");
  return words.charAt(0).toUpperCase() + words.slice(1);
}

/**
 * The one short line under the name: the registry's summary. A setting
 * added with none shows the first sentence of its description.
 */
export function shortLineOf(s: Pick<Entry, "summary" | "description">): string {
  if (s.summary && s.summary.trim() !== "") return s.summary;
  const d = s.description.trim();
  // A sentence ends at a full stop that a space follows, so "0.5" and
  // "docs/x.md" do not end one.
  const end = d.search(/\.(\s|$)/);
  return end < 0 ? d : d.slice(0, end + 1);
}

// The headings. A setting goes under the heading of the longest prefix of
// its key listed here, so a lane that adds "agents.task_something" lands
// under "Background tasks and sub-agents" with no change to this file. The
// order of the list is the order of the headings on the screen: what a
// person changes most comes first, and what only an install has comes last.
// A key no prefix covers gets a heading made from its first word, after
// these.
const headings: [prefix: string, heading: string][] = [
  ["appearance.", "Appearance"],

  ["chats.", "Chats"],
  ["knowledge.incognito_", "Chats"],
  ["chats.recap", "Finding past chats"],
  ["chats.past_chats_", "Finding past chats"],
  ["chats.search_", "Finding past chats"],
  ["chats.context_", "What a model is sent"],
  ["chats.compact", "Long chats"],
  ["voice.", "Dictation"],
  ["styles.", "Styles"],
  ["projects.", "Projects"],
  ["uploads.", "Attached files"],
  ["artifacts.", "Artifacts"],
  ["share.", "Shared links"],

  ["routing.", "Models"],
  ["models.", "Models"],

  ["knowledge.", "Knowledge store"],
  ["knowledge.recall", "Recall"],
  ["knowledge.memory", "Memory"],
  ["knowledge.embedding", "Search by meaning"],

  ["agents.", "Tools"],
  ["agents.web_", "Web search and pages"],
  ["agents.connector", "Connectors"],
  ["agents.load_tools_", "Connectors"],
  ["agents.google_", "Google"],
  ["agents.task_", "Background tasks and sub-agents"],
  ["agents.subagent", "Background tasks and sub-agents"],
  ["agents.pause", "Background tasks and sub-agents"],
  ["agents.unattended_", "Background tasks and sub-agents"],
  ["agents.schedule", "Scheduled tasks"],
  ["agents.time_zone", "Scheduled tasks"],
  ["agents.wiki_", "Wiki jobs"],

  ["usage.", "Usage and spending"],
  ["operations.", "Operations"],

  ["signin.", "Signing in"],
  ["server.", "The server"],
  ["data.", "The server"],
  ["secrets.", "The server"],
  ["test_tenant.", "The server"],
  ["backup.", "Backups"],
  ["chats.import_", "Import from New Keystone"],
  ["chats.test_model_", "The test person"],
  ["fake.", "The test person"],
  ["budget.", "Token estimates"],
  ["anthropic.", "Model adapters"],
  ["responses.", "Model adapters"],
  ["chat_completions.", "Model adapters"],
];

/** The heading a setting goes under. */
export function headingOf(key: string): string {
  let best = "";
  let heading = "";
  for (const [prefix, h] of headings) {
    if (key.startsWith(prefix) && prefix.length > best.length) {
      best = prefix;
      heading = h;
    }
  }
  if (heading !== "") return heading;
  const first = key.split(".")[0].replace(/_/g, " ");
  return first.charAt(0).toUpperCase() + first.slice(1);
}

export type Group<T> = { heading: string; settings: T[] };

/**
 * Settings under their headings: the headings in the order of the list
 * above, then any others by name, and the settings of each by key, which
 * keeps the ones that belong together side by side. Every setting given is
 * in exactly one group.
 */
export function groupsOf<T extends Pick<Entry, "key">>(settings: T[]): Group<T>[] {
  const order = [...new Set(headings.map(([, h]) => h))];
  const byHeading = new Map<string, T[]>();
  for (const s of settings) {
    const h = headingOf(s.key);
    byHeading.set(h, [...(byHeading.get(h) ?? []), s]);
  }
  const known = order.filter((h) => byHeading.has(h));
  const others = [...byHeading.keys()].filter((h) => !order.includes(h)).sort();
  return [...known, ...others].map((heading) => ({
    heading,
    settings: byHeading.get(heading)!.sort((a, b) => (a.key < b.key ? -1 : a.key > b.key ? 1 : 0)),
  }));
}

/**
 * Whether a search finds a setting. Every word typed must be in its name,
 * its short line, its key or its heading, in any case. A key may be typed
 * with spaces for its dots and underscores. Nothing typed finds everything.
 */
export function found(s: Pick<Entry, "key" | "title" | "summary" | "description">, query: string): boolean {
  const words = query.toLowerCase().split(/\s+/).filter(Boolean);
  if (words.length === 0) return true;
  const hay = [titleOf(s), shortLineOf(s), s.key, s.key.replace(/[._]/g, " "), headingOf(s.key)].join("\n").toLowerCase();
  return words.every((w) => hay.includes(w));
}

/** Which control a setting is changed with. The type and the options alone choose it. */
export type Control = "switch" | "select" | "number" | "duration" | "text" | "long-text";

/** A text longer than this, or of several lines, is edited in a box of several lines. */
const longTextFrom = 60;

export function controlOf(s: Pick<Entry, "type" | "options" | "default" | "value">): Control {
  if (s.type === "bool") return "switch";
  if (s.options.length > 0) return "select";
  if (s.type === "int" || s.type === "float") return "number";
  if (s.type === "duration") return "duration";
  if (s.default.length > longTextFrom || s.value.length > longTextFrom || s.value.includes("\n")) return "long-text";
  return "text";
}

/** How a value reads in a line of text: a switch as a word, nothing as "Empty", a long text cut short. */
export function valueText(s: Pick<Entry, "type">, value: string, most = 48): string {
  if (s.type === "bool") return value === "true" ? "On" : "Off";
  const one = value.replace(/\s+/g, " ").trim();
  if (one === "") return "Empty";
  const chars = [...one];
  return chars.length > most ? chars.slice(0, most - 1).join("").trimEnd() + "…" : one;
}

/** What a value's control accepts, said in a few words under it. */
export function formatHint(s: Pick<Entry, "type" | "options">): string {
  if (s.options.length > 0) return "";
  if (s.type === "int") return "A whole number.";
  if (s.type === "float") return "A number.";
  if (s.type === "duration") return "A length of time, such as 30s, 5m or 1h.";
  return "";
}
