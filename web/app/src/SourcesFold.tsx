// A list of sources, closed until the person opens it: one line, "3 sources",
// with a caret. Markdown.tsx folds a reply's or a document's own list
// (foldSources.ts finds it), and Research.tsx folds a task's.

import { useState, type ReactNode } from "react";
import { Icon } from "./Icon";
import { sourcesLabel } from "./foldSources";
import "./sources-fold.css";

export function SourcesFold({ count, children }: { count: number; children: ReactNode }) {
  const [open, setOpen] = useState(false);
  return (
    <div className={open ? "sources-fold is-open" : "sources-fold"}>
      <button type="button" className="sources-fold-head" aria-expanded={open} onClick={() => setOpen((o) => !o)}>
        <Icon name="link" size={14} />
        <span>{sourcesLabel(count)}</span>
        <span className={open ? "step-caret is-open" : "step-caret"}>
          <Icon name="caret" size={14} />
        </span>
      </button>
      {open && <div className="sources-fold-body">{children}</div>}
    </div>
  );
}
