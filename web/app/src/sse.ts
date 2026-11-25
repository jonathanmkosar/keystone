// A reader for server-sent events. It makes no call itself: api.ts opens the
// stream with fetch and hands the body here. fetch is used, not EventSource,
// so the caller sees the status of a refused stream and decides for itself
// when to connect again and with which Last-Event-ID.

export type StreamEvent = {
  /** The id the server gave this event, or "" when it gave none. */
  id: string;
  /** The event's name. "message" when the server gave none. */
  name: string;
  /** The data, as sent. Keystone sends JSON on one line. */
  data: string;
};

/** Turns chunks of text into events. Feed it text as it arrives. */
export function eventParser(onEvent: (e: StreamEvent) => void): (chunk: string) => void {
  let rest = "";
  let id = "";
  let name = "";
  let data: string[] = [];

  function line(text: string) {
    if (text === "") {
      // A blank line ends an event. One with no data is not an event.
      if (data.length > 0) onEvent({ id, name: name || "message", data: data.join("\n") });
      id = "";
      name = "";
      data = [];
      return;
    }
    if (text.startsWith(":")) return; // a comment: the server's heartbeat
    const colon = text.indexOf(":");
    const field = colon < 0 ? text : text.slice(0, colon);
    let value = colon < 0 ? "" : text.slice(colon + 1);
    if (value.startsWith(" ")) value = value.slice(1);
    if (field === "data") data.push(value);
    else if (field === "event") name = value;
    else if (field === "id" && !value.includes("\0")) id = value;
  }

  return (chunk: string) => {
    rest += chunk;
    for (;;) {
      const at = rest.search(/[\r\n]/);
      if (at < 0) return;
      // A carriage return at the very end may be half of a CRLF. Wait for more.
      if (rest[at] === "\r" && at === rest.length - 1) return;
      const end = rest[at] === "\r" && rest[at + 1] === "\n" ? at + 2 : at + 1;
      const text = rest.slice(0, at);
      rest = rest.slice(end);
      line(text);
    }
  };
}

/**
 * Reads a response body, calling onEvent for each event in it. It reads to
 * the body's end, or until onEvent returns false: the caller has seen the
 * stream's last event and does not wait for the server to close.
 */
export async function readEvents(body: ReadableStream<Uint8Array>, onEvent: (e: StreamEvent) => boolean | void): Promise<void> {
  let stopped = false;
  const feed = eventParser((e) => {
    if (!stopped && onEvent(e) === false) stopped = true;
  });
  const reader = body.getReader();
  const decoder = new TextDecoder();
  try {
    for (;;) {
      const { done, value } = await reader.read();
      if (done) return;
      feed(decoder.decode(value, { stream: true }));
      if (stopped) {
        await reader.cancel().catch(() => {});
        return;
      }
    }
  } finally {
    reader.releaseLock();
  }
}
