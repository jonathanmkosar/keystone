// A list of sources in a reply or a document is folded away, closed until
// the person opens it. This file finds the list in the text: a line that
// says "Sources" (or "References", "Citations", "Works cited"), as a heading,
// in bold or with a colon, followed by list items or bare addresses. The
// importer writes New Keystone's sources that way ("Sources:" and "- [n]
// [title](url)"), and a research report ends with "## Sources" and a
// numbered list. Text after the list, such as the report's closing line,
// stays where it is. A line inside a fenced code block is never a heading.

export type SourcesFold = {
  /** The text before the heading. */
  before: string;
  /** The list, as Markdown. */
  list: string;
  /** The text after the list. */
  after: string;
  /** How many sources the list holds. */
  count: number;
};

const heading = /^\s{0,3}(?:#{1,6}\s+)?(?:\*\*|__)?\s*(?:sources|references|citations|works cited|sources cited)\s*:?\s*(?:\*\*|__)?\s*:?\s*$/i;
const item = /^\s*(?:[-*+]|\d{1,4}[.)]|\[\d{1,4}\])\s+\S/;
const bareAddress = /^\s*<?https?:\/\/\S+>?\s*$/;
const fence = /^\s{0,3}(```|~~~)/;

function isItem(line: string): boolean {
  return item.test(line) || bareAddress.test(line);
}

/** The list of sources in the text, or null when it has none. The last such list wins. */
export function foldSources(text: string): SourcesFold | null {
  const lines = text.replace(/\r\n/g, "\n").split("\n");
  let inFence = false;
  let found: SourcesFold | null = null;
  for (let i = 0; i < lines.length; i++) {
    if (fence.test(lines[i])) {
      inFence = !inFence;
      continue;
    }
    if (inFence || !heading.test(lines[i])) continue;
    let end = i + 1;
    let count = 0;
    let last = i; // the last line that belongs to the list
    for (; end < lines.length; end++) {
      const line = lines[end];
      if (line.trim() === "") continue;
      if (isItem(line)) {
        count++;
        last = end;
        continue;
      }
      // A line indented under an item carries on that item.
      if (count > 0 && /^\s{2,}\S/.test(line) && !fence.test(line)) {
        last = end;
        continue;
      }
      break;
    }
    if (count === 0) continue;
    found = {
      before: lines.slice(0, i).join("\n").replace(/\s+$/, ""),
      list: lines.slice(i + 1, last + 1).join("\n").replace(/^\s*\n/, ""),
      after: lines.slice(last + 1).join("\n").replace(/^\s+/, ""),
      count,
    };
    i = last;
  }
  return found;
}

/** "3 sources", "1 source". */
export function sourcesLabel(count: number): string {
  return count === 1 ? "1 source" : `${count} sources`;
}
