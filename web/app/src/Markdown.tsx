// A small reader for the Markdown a model writes: paragraphs, headings,
// lists, quotes, rules, tables, code blocks, and inside a line code, bold,
// italic, struck text and links. It builds React elements and never sets
// HTML from text, so nothing a model writes can become markup or script.
// Decision 0150 says why this is written here and not taken from a package.

import { useEffect, useRef, useState, type ReactNode } from "react";
import { Icon } from "./Icon";

type Block =
  | { kind: "p"; text: string }
  | { kind: "h"; level: number; text: string }
  | { kind: "code"; lang: string; text: string }
  | { kind: "list"; ordered: boolean; start: number; items: string[] }
  | { kind: "quote"; text: string }
  | { kind: "rule" }
  | { kind: "table"; head: string[]; rows: string[][] };

const fence = /^ {0,3}(`{3,}|~{3,})\s*([^\s`]*)/;
const heading = /^ {0,3}(#{1,6})\s+(.*?)\s*#*\s*$/;
const rule = /^ {0,3}([-*_])(?:\s*\1){2,}\s*$/;
const bullet = /^( {0,3})([-*+])\s+(.*)$/;
const numbered = /^( {0,3})(\d{1,9})[.)]\s+(.*)$/;
const quote = /^ {0,3}>\s?(.*)$/;
const tableDivider = /^\s*\|?\s*:?-{1,}:?\s*(\|\s*:?-{1,}:?\s*)*\|?\s*$/;

function cells(line: string): string[] {
  let text = line.trim();
  if (text.startsWith("|")) text = text.slice(1);
  if (text.endsWith("|") && !text.endsWith("\\|")) text = text.slice(0, -1);
  return text.split(/(?<!\\)\|/).map((c) => c.trim().replace(/\\\|/g, "|"));
}

function startsBlock(line: string): boolean {
  return fence.test(line) || heading.test(line) || rule.test(line) || bullet.test(line) || numbered.test(line) || quote.test(line);
}

function parse(source: string): Block[] {
  const lines = source.replace(/\r\n?/g, "\n").split("\n");
  const out: Block[] = [];
  let i = 0;
  while (i < lines.length) {
    const line = lines[i];
    if (line.trim() === "") {
      i++;
      continue;
    }

    const f = fence.exec(line);
    if (f) {
      const close = new RegExp("^ {0,3}" + f[1][0] + "{" + f[1].length + ",}\\s*$");
      const body: string[] = [];
      i++;
      // A block with no closing fence runs to the end: that is a reply still
      // being written.
      while (i < lines.length && !close.test(lines[i])) body.push(lines[i++]);
      i++;
      out.push({ kind: "code", lang: f[2], text: body.join("\n") });
      continue;
    }

    const h = heading.exec(line);
    if (h) {
      out.push({ kind: "h", level: h[1].length, text: h[2] });
      i++;
      continue;
    }

    if (rule.test(line)) {
      out.push({ kind: "rule" });
      i++;
      continue;
    }

    if (quote.test(line)) {
      const body: string[] = [];
      while (i < lines.length && quote.test(lines[i])) body.push(quote.exec(lines[i++])![1]);
      out.push({ kind: "quote", text: body.join("\n") });
      continue;
    }

    const first = bullet.exec(line) ?? numbered.exec(line);
    if (first) {
      const ordered = !bullet.test(line);
      const marker = ordered ? numbered : bullet;
      const items: string[] = [];
      while (i < lines.length) {
        const m = marker.exec(lines[i]);
        if (!m) break;
        // What belongs to an item: its first line, then every line indented
        // under it. Blank lines are kept only when more of the item follows.
        const indent = m[1].length + 2;
        const body = [m[3]];
        i++;
        while (i < lines.length) {
          const next = lines[i];
          if (next.trim() === "") {
            const after = lines[i + 1];
            if (after !== undefined && after.trim() !== "" && /^\s/.test(after) && after.search(/\S/) >= indent) {
              body.push("");
              i++;
              continue;
            }
            break;
          }
          if (next.search(/\S/) >= indent) body.push(next.slice(indent));
          else if (!startsBlock(next) && !marker.test(next)) body.push(next.trim());
          else break;
          i++;
        }
        items.push(body.join("\n"));
        // One blank line between items keeps the list whole.
        if (i < lines.length && lines[i].trim() === "" && i + 1 < lines.length && marker.test(lines[i + 1])) i++;
      }
      out.push({ kind: "list", ordered, start: ordered ? Number(first[2]) : 1, items });
      continue;
    }

    if (line.includes("|") && i + 1 < lines.length && tableDivider.test(lines[i + 1]) && lines[i + 1].includes("-")) {
      const head = cells(line);
      const rows: string[][] = [];
      i += 2;
      while (i < lines.length && lines[i].trim() !== "" && lines[i].includes("|")) rows.push(cells(lines[i++]));
      out.push({ kind: "table", head, rows });
      continue;
    }

    const body = [line];
    i++;
    while (i < lines.length && lines[i].trim() !== "" && !startsBlock(lines[i])) body.push(lines[i++]);
    out.push({ kind: "p", text: body.join("\n") });
  }
  return out;
}

// Inside a line. One pattern, tried left to right:
// 1 an escaped mark, 2-3 code, 4-5 a link, 6 bold, 7 bold with underscores,
// 8 italic, 9 italic with underscores, 10 struck, 11 a bare address.
const inlinePattern =
  /\\([\\`*_~[\]()#>!.+-])|(`+)(?!`)([\s\S]*?[^`])\2(?!`)|\[([^\]\n]+)\]\(\s*([^()\s]+(?:\([^()\s]*\)[^()\s]*)*)(?:\s+"[^"]*")?\s*\)|\*\*(?=\S)([\s\S]*?\S)\*\*|(?<![A-Za-z0-9])__(?=\S)([\s\S]*?\S)__(?![A-Za-z0-9])|\*(?=[^\s*])([\s\S]*?[^\s*])\*(?!\*)|(?<![A-Za-z0-9_])_(?=[^\s_])([\s\S]*?[^\s_])_(?![A-Za-z0-9_])|~~(?=\S)([\s\S]*?\S)~~|(https?:\/\/[^\s<>"'`]*[^\s<>"'`.,;:!?)\]}])/g;

/** An address a reply may link to. Anything else is shown as plain text. */
function safeAddress(href: string): boolean {
  return /^(https?:\/\/|mailto:)/i.test(href);
}

function inline(text: string, keyBase = ""): ReactNode[] {
  const out: ReactNode[] = [];
  const pattern = new RegExp(inlinePattern.source, "g");
  let last = 0;
  let n = 0;
  for (let m = pattern.exec(text); m; m = pattern.exec(text)) {
    if (m.index > last) out.push(text.slice(last, m.index));
    const key = keyBase + "i" + n++;
    if (m[1] !== undefined) out.push(m[1]);
    else if (m[3] !== undefined) out.push(<code key={key}>{m[3].replace(/^ (.*) $/s, "$1")}</code>);
    else if (m[4] !== undefined) {
      if (safeAddress(m[5])) {
        out.push(
          <a key={key} href={m[5]} target="_blank" rel="noopener noreferrer">
            {inline(m[4], key)}
          </a>,
        );
      } else out.push(m[0]);
    } else if (m[6] !== undefined || m[7] !== undefined) out.push(<strong key={key}>{inline(m[6] ?? m[7], key)}</strong>);
    else if (m[8] !== undefined || m[9] !== undefined) out.push(<em key={key}>{inline(m[8] ?? m[9], key)}</em>);
    else if (m[10] !== undefined) out.push(<del key={key}>{inline(m[10], key)}</del>);
    else if (m[11] !== undefined) {
      out.push(
        <a key={key} href={m[11]} target="_blank" rel="noopener noreferrer">
          {m[11]}
        </a>,
      );
    }
    last = m.index + m[0].length;
  }
  if (last < text.length) out.push(text.slice(last));
  return out;
}

async function copyText(text: string): Promise<boolean> {
  try {
    // The clipboard is offered only on localhost and over HTTPS.
    if (navigator.clipboard) {
      await navigator.clipboard.writeText(text);
      return true;
    }
  } catch {
    // Fall through to the older way.
  }
  const area = document.createElement("textarea");
  area.value = text;
  area.setAttribute("readonly", "");
  area.className = "offscreen";
  document.body.appendChild(area);
  area.select();
  let ok = false;
  try {
    ok = document.execCommand("copy");
  } catch {
    ok = false;
  }
  area.remove();
  return ok;
}

/** A button that copies text and says so for a moment. */
export function CopyButton({ text, label = "Copy" }: { text: string; label?: string }) {
  const [state, setState] = useState<"idle" | "copied" | "failed">("idle");
  const timer = useRef<number | undefined>(undefined);
  useEffect(() => () => window.clearTimeout(timer.current), []);
  async function copy() {
    setState((await copyText(text)) ? "copied" : "failed");
    window.clearTimeout(timer.current);
    timer.current = window.setTimeout(() => setState("idle"), 1600);
  }
  return (
    <button type="button" className="copy-button" onClick={copy} aria-live="polite">
      <Icon name={state === "copied" ? "check" : "copy"} size={14} />
      {state === "copied" ? "Copied" : state === "failed" ? "Could not copy" : label}
    </button>
  );
}

function CodeBlock({ lang, text }: { lang: string; text: string }) {
  return (
    <div className="code-block">
      <div className="code-head">
        <span className="code-lang">{lang || "code"}</span>
        <CopyButton text={text} />
      </div>
      <pre>
        <code>{text}</code>
      </pre>
    </div>
  );
}

function render(blocks: Block[], keyBase: string): ReactNode[] {
  return blocks.map((b, i) => {
    const key = keyBase + i;
    switch (b.kind) {
      case "p":
        return <p key={key}>{inline(b.text)}</p>;
      case "h": {
        // A reply's headings sit under the page's own, so they start at h2.
        const level = Math.min(b.level + 1, 6);
        const Tag = `h${level}` as "h2" | "h3" | "h4" | "h5" | "h6";
        return (
          <Tag key={key} className={"md-h md-h" + b.level}>
            {inline(b.text)}
          </Tag>
        );
      }
      case "code":
        return <CodeBlock key={key} lang={b.lang} text={b.text} />;
      case "rule":
        return <hr key={key} />;
      case "quote":
        return <blockquote key={key}>{render(parse(b.text), key + "q")}</blockquote>;
      case "list": {
        const items = b.items.map((item, n) => {
          const inner = parse(item);
          const only = inner.length === 1 && inner[0].kind === "p" ? inner[0] : undefined;
          return <li key={n}>{only ? inline(only.text) : render(inner, key + "l" + n)}</li>;
        });
        return b.ordered ? (
          <ol key={key} start={b.start}>
            {items}
          </ol>
        ) : (
          <ul key={key}>{items}</ul>
        );
      }
      case "table":
        return (
          <div key={key} className="table-wrap">
            <table>
              <thead>
                <tr>
                  {b.head.map((c, n) => (
                    <th key={n}>{inline(c)}</th>
                  ))}
                </tr>
              </thead>
              <tbody>
                {b.rows.map((row, r) => (
                  <tr key={r}>
                    {b.head.map((_, n) => (
                      <td key={n}>{inline(row[n] ?? "")}</td>
                    ))}
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        );
    }
  });
}

/** A model's text, shown as it reads. Safe to call again as the text grows. */
export function Markdown({ text }: { text: string }) {
  return <div className="md">{render(parse(text), "b")}</div>;
}
