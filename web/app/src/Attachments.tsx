// Uploads on the screen (P9-02): the composer's attach button, dropping
// files on it and the chips of what will be sent; images as thumbnails and
// files by name in the person's message; the notice on a reply that did
// not see an image; and what each call was given, in the turn inspector.
//
// There is no mockup for uploads (P1-06 is not approved), so all of it is
// built from the design tokens and the composer of design/screens, whose
// "Add files or a chat" button and chips of Home.html it follows, and it
// is provisional. The content security policy allows images from
// Keystone's own address only, so a file held before sending shows its
// name and size, and a thumbnail once the server has it.

import { useEffect, useId, useRef, useState, type DragEvent } from "react";
import type { Block } from "./api";
import { filesState } from "./composerSheet";
import { Icon } from "./Icon";
import { SheetRow, useSheet } from "./PhoneSheet";
import { acceptOf, contentURL, refusal, size, uploadLimits, type ChatUploads, type Limits, type Notice } from "./uploads";
import "./uploads.css";

/** What the composer holds of the files to send. */
export type Attachments = {
  files: File[];
  /** Why a file was not taken, for a person. Empty otherwise. */
  problem: string;
  limits?: Limits;
  /** Files are being dragged over the composer. */
  dragging: boolean;
  add: (list: FileList | File[]) => void;
  remove: (index: number) => void;
  clear: () => void;
  dropProps: {
    onDragOver: (e: DragEvent<HTMLElement>) => void;
    onDragLeave: (e: DragEvent<HTMLElement>) => void;
    onDrop: (e: DragEvent<HTMLElement>) => void;
  };
};

function holdsFiles(e: DragEvent<HTMLElement>): boolean {
  return Array.from(e.dataTransfer.types).includes("Files");
}

function same(a: File, b: File): boolean {
  return a.name === b.name && a.size === b.size && a.lastModified === b.lastModified;
}

/** The files a composer will send with its message, checked against what the server takes. */
export function useAttachments(): Attachments {
  const [files, setFiles] = useState<File[]>([]);
  const [problem, setProblem] = useState("");
  const [limits, setLimits] = useState<Limits>();
  const [dragging, setDragging] = useState(false);

  useEffect(() => {
    let live = true;
    uploadLimits().then(
      (l) => live && setLimits(l),
      () => {}, // the server says no when the file is sent
    );
    return () => {
      live = false;
    };
  }, []);

  function add(list: FileList | File[]) {
    const take: File[] = [];
    const said: string[] = [];
    for (const f of Array.from(list)) {
      const why = refusal(f, limits);
      if (why) said.push(why);
      else take.push(f);
    }
    setProblem(said.join(" "));
    if (take.length > 0) setFiles((have) => [...have, ...take.filter((f) => !have.some((h) => same(h, f)))]);
  }

  return {
    files,
    problem,
    limits,
    dragging,
    add,
    remove: (i) => {
      setFiles((have) => have.filter((_, j) => j !== i));
      setProblem("");
    },
    clear: () => {
      setFiles([]);
      setProblem("");
    },
    dropProps: {
      onDragOver: (e) => {
        if (!holdsFiles(e)) return;
        e.preventDefault();
        e.dataTransfer.dropEffect = "copy";
        setDragging(true);
      },
      onDragLeave: (e) => {
        if (!e.currentTarget.contains(e.relatedTarget as Node | null)) setDragging(false);
      },
      onDrop: (e) => {
        if (!holdsFiles(e)) return;
        e.preventDefault();
        setDragging(false);
        add(e.dataTransfer.files);
      },
    },
  };
}

function endingOf(name: string): string {
  const dot = name.lastIndexOf(".");
  return dot > 0 && name.length - dot <= 6 ? name.slice(dot + 1).toUpperCase() : "FILE";
}

/** The composer's attach button, with the file picker it opens. In the phone's + sheet it is a row (decision 0590). */
export function AttachButton({ attach, disabled = false }: { attach: Attachments; disabled?: boolean }) {
  const picker = useRef<HTMLInputElement>(null);
  const id = useId();
  const sheet = useSheet();
  return (
    <>
      <input
        id={id}
        ref={picker}
        type="file"
        multiple
        className="offscreen"
        tabIndex={-1}
        accept={acceptOf(attach.limits)}
        onChange={(e) => {
          if (e.target.files) attach.add(e.target.files);
          e.target.value = ""; // the same file may be picked again after it is removed
        }}
      />
      {sheet ? (
        <div className="sheet-item">
          <SheetRow
            icon="file"
            name="Files or photos"
            state={filesState(attach.files.length)}
            disabled={disabled}
            onClick={() => {
              picker.current?.click();
              sheet.done();
            }}
          />
        </div>
      ) : (
        <button type="button" className="icon-button attach-button" aria-label="Attach files" title="Attach files" disabled={disabled} onClick={() => picker.current?.click()}>
          <Icon name="plus" size={18} />
        </button>
      )}
    </>
  );
}

/** The chips of the files that go with the message, each with its remove button, and why a file was not taken. */
export function AttachChips({ attach }: { attach: Attachments }) {
  if (attach.files.length === 0 && !attach.problem && !attach.dragging) return null;
  return (
    <div className="attach-row">
      {attach.dragging && <p className="attach-drop">Drop the files to attach them</p>}
      {attach.files.length > 0 && (
        <ul className="attach-chips" aria-label="Files to send">
          {attach.files.map((f, i) => (
            <li key={`${f.name}:${f.size}:${f.lastModified}`} className="attach-chip">
              <span className="attach-chip-kind" aria-hidden="true">
                {endingOf(f.name)}
              </span>
              <span className="attach-chip-name" title={f.name}>
                {f.name}
              </span>
              <span className="attach-chip-size">{size(f.size)}</span>
              <button type="button" className="icon-button attach-chip-remove" aria-label={`Remove ${f.name}`} onClick={() => attach.remove(i)}>
                <Icon name="close" size={14} />
              </button>
            </li>
          ))}
        </ul>
      )}
      {attach.problem && (
        <p className="composer-problem" role="alert">
          {attach.problem}
        </p>
      )}
    </div>
  );
}

/** The images and files of a person's message: images as thumbnails that open whole, files by name, as downloads. */
export function SaidMedia({ chatId, blocks, files, known }: { chatId: string; blocks?: Block[]; files: string[]; known: ChatUploads }) {
  const media = (blocks ?? []).filter((b) => (b.type === "image" || b.type === "file") && b.ref);
  if (media.length === 0) {
    // Files sent with their bytes, or a message not in the record yet.
    if (files.length === 0) return null;
    return (
      <div className="said-files">
        {files.map((f, i) => (
          <span key={i} className="said-file">
            {f}
          </span>
        ))}
      </div>
    );
  }
  const images = media.filter((b) => b.type === "image");
  const others = media.filter((b) => b.type !== "image");
  return (
    <>
      {images.length > 0 && (
        <div className="said-images">
          {images.map((b) => {
            const name = known.uploads.get(b.ref!)?.name ?? "An image";
            const href = contentURL(chatId, b.ref!);
            return (
              <a key={b.ref} className="said-image-link" href={href} target="_blank" rel="noopener noreferrer" title={name}>
                <img className="said-image" src={href} alt={name} loading="lazy" />
              </a>
            );
          })}
        </div>
      )}
      {others.length > 0 && (
        <div className="said-files">
          {others.map((b) => (
            <a key={b.ref} className="said-file said-file-link" href={contentURL(chatId, b.ref!)} download={b.name}>
              {b.name || known.uploads.get(b.ref!)?.name || "A file"}
            </a>
          ))}
        </div>
      )}
    </>
  );
}

/** What the person is told about one reply: an image it did not see, or the fallback that answered to see one. */
export function ReplyNotices({ notices }: { notices: Notice[] }) {
  if (notices.length === 0) return null;
  return (
    <div className="reply-notices">
      {notices.map((n, i) => (
        <p key={i} className={n.kind === "moved" ? "reply-notice" : "reply-notice is-withheld"} role="note">
          <Icon name={n.kind === "moved" ? "retry" : "alert"} size={16} />
          <span>{n.text}</span>
        </p>
      ))}
    </div>
  );
}

/** One call's record of a chat's uploads, as the turn inspector returns it. */
export type UploadCall = {
  step: number;
  call: {
    model: string;
    sees: boolean;
    files: boolean;
    moved?: string;
    items: { id?: string; name: string; kind: string; current?: boolean; sent: string; why?: string; chars?: number; cut?: boolean }[];
  };
};

const whyWords: Record<string, string> = {
  vision: "withheld: a model this call could reach cannot see images",
  files: "withheld: a model this call could reach takes no files, and Keystone cannot read its text yet",
  missing: "withheld: it is no longer stored",
};

function sentWords(it: UploadCall["call"]["items"][number]): string {
  switch (it.sent) {
    case "image":
      return "sent as an image";
    case "file":
      return "sent whole, as a file";
    case "text":
      return `its text with the attachments, ${(it.chars ?? 0).toLocaleString("en-US")} characters${it.cut ? ", cut" : ""}`;
  }
  return whyWords[it.why ?? ""] ?? "withheld";
}

/** The turn inspector's section on what the calls were given of the chat's images and files. */
export function UploadsSection({ calls }: { calls: UploadCall[] | undefined }) {
  const first = calls?.[0];
  if (!first) return null;
  const items = first.call.items ?? [];
  return (
    <section className="inspector-section" aria-label="Images and files">
      <h3 className="inspector-heading">Images and files</h3>
      <p className="inspector-note">
        {first.call.sees ? "Every model this call could reach can see images." : "A model this call could reach cannot see images, so no image was sent."}
        {first.call.moved && ` ${first.call.moved} cannot see images, so the call went to ${first.call.model}.`}
      </p>
      {items.length > 0 && (
        <ul className="inspector-items">
          {items.map((it, i) => (
            <li key={`${it.id ?? ""}:${i}`} className={it.sent === "withheld" ? "inspector-item is-removed" : "inspector-item"}>
              <div className="inspector-item-text">
                <span className="inspector-item-label">{it.name}</span>
                <span className="inspector-item-meta">
                  {it.kind}
                  {it.current ? " · in this message" : " · from an earlier message"} · {sentWords(it)}
                </span>
              </div>
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}
