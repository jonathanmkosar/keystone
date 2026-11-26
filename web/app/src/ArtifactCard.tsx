// An artifact's card in a reply (P9-03): what create_artifact or
// update_artifact made, by its title, type and version, which opens the
// artifact in the panel beside the chat. Drawn in the reply's steps in
// place of the tool's row, from the call's input and the words of its
// result, so it is the same after a reload. A call that failed keeps its
// row. Provisional with the panel (P1-06).

import { useParams } from "react-router";
import { artifactOfResult, openArtifact, typeLabel, useOpenArtifact } from "./artifacts";
import { Icon } from "./Icon";

type ToolStep = { id: string; name: string; input: string; running: boolean; result?: string; failed?: boolean };

/** Whether a tool step is drawn as an artifact's card. */
export function isArtifactCard(step: ToolStep): boolean {
  return (step.name === "create_artifact" || step.name === "update_artifact") && !step.failed;
}

function inputOf(raw: string): { type?: string; title?: string; language?: string; artifact_id?: string } {
  try {
    const v = JSON.parse(raw) as unknown;
    return typeof v === "object" && v !== null ? (v as Record<string, string>) : {};
  } catch {
    return {};
  }
}

/** The title a result gives a version: “Title” after Made or Wrote the artifact. */
function titleOf(result: string | undefined): string | undefined {
  return /the artifact “([^”]*)”/.exec(result ?? "")?.[1];
}

function typeOf(result: string | undefined): string | undefined {
  return /, type ([a-z]+)/.exec(result ?? "")?.[1];
}

export function ArtifactCard({ step }: { step: ToolStep }) {
  const { id: chatId = "" } = useParams();
  const input = inputOf(step.input);
  const made = artifactOfResult(step.result);
  const open = useOpenArtifact(chatId);
  const title = titleOf(step.result) ?? input.title ?? "An artifact";
  const type = input.type ?? typeOf(step.result) ?? "";
  const isOpen = made !== undefined && open?.artifactId === made.id;
  const what = step.running ? (step.name === "create_artifact" ? "Making" : "Changing") : made ? `${typeLabel(type, input.language)} · version ${made.version}` : "";
  const body = (
    <>
      <span className="artifact-card-icon">
        <Icon name="artifacts" size={20} />
      </span>
      <span className="artifact-card-text">
        <span className="artifact-card-title">{title}</span>
        <span className="artifact-card-meta">{what}</span>
      </span>
      {made && <span className="artifact-card-open">{isOpen ? "Open" : "Open beside the chat"}</span>}
    </>
  );
  if (!made || chatId === "") {
    return (
      <div className={step.running ? "artifact-card is-running" : "artifact-card"} role={step.running ? "status" : undefined}>
        {body}
      </div>
    );
  }
  return (
    <button type="button" className={isOpen ? "artifact-card is-open" : "artifact-card"} aria-pressed={isOpen} onClick={() => openArtifact(chatId, made.id, made.version)}>
      {body}
    </button>
  );
}
