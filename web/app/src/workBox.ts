// What the closed work box of a reply says on its one line: the thinking
// and the tool calls between two stretches of text are one box, closed
// until the person opens it. While the reply runs, the line is what is
// happening now; once it is done, a summary: how long it thought and how
// many tools it used, or the one tool's own line when there was only one.
// WorkBox.tsx draws it; steps.ts writes each tool's lines.

export type WorkEntry =
  | { kind: "thinking"; running: boolean; seconds?: number }
  | { kind: "tool"; running: boolean; failed?: boolean; waiting?: boolean; line: { running: string; asked: string; done: string } };

function seconds(n: number): string {
  const whole = Math.max(1, Math.round(n));
  return whole === 1 ? "1 second" : `${whole} seconds`;
}

/** The box's line. live: the reply is still being written. */
export function workLabel(entries: WorkEntry[], live: boolean): string {
  const last = entries[entries.length - 1];
  if (live && last && last.running) {
    if (last.kind === "thinking") return "Thinking";
    return last.waiting ? `${last.line.asked}: waiting for you` : last.line.running;
  }
  const tools = entries.filter((e): e is Extract<WorkEntry, { kind: "tool" }> => e.kind === "tool");
  const thoughts = entries.filter((e): e is Extract<WorkEntry, { kind: "thinking" }> => e.kind === "thinking");
  const failed = tools.filter((t) => t.failed).length;
  const timed = thoughts.filter((t) => t.seconds !== undefined);
  const thought = thoughts.length === 0 ? "" : timed.length > 0 ? `Thought for ${seconds(timed.reduce((n, t) => n + (t.seconds ?? 0), 0))}` : "Thought";
  let used = "";
  if (tools.length === 1) used = tools[0].failed ? tools[0].line.asked : tools[0].line.done;
  else if (tools.length > 1) used = `Used ${tools.length} tools`;
  if (thought !== "" && used !== "") used = tools.length === 1 ? used.charAt(0).toLowerCase() + used.slice(1) : `used ${tools.length} tools`;
  const parts = [thought, used].filter(Boolean);
  let label = parts.join(", ") || "Worked";
  if (failed > 0 && tools.length > 1) label += ` (${failed} failed)`;
  return label;
}
