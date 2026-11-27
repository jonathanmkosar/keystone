// The mark of a reply that is still being written: three copper arcs, in the
// mark's three coppers, turning round its keystone. It sits at the foot of
// the reply for as long as the turn runs, as the owner asked on November 27,
// 2026 ("spin graphic while thinking and not up top"). The look is in
// chat.css; with reduced motion it stands still.

const keystone = "M26.8 13.5A27 27 0 0 1 37.2 13.5L33.3 33.5A6.6 6.6 0 0 0 30.7 33.5Z";

/** The turning mark, with words beside it when there are any. */
export function ReplySpinner({ words }: { words?: string }) {
  return (
    <div className="reply-spinner" role="status" aria-label={words ? `Keystone is ${words.toLowerCase()}` : "Keystone is working"}>
      <svg className="spinner" width="28" height="28" viewBox="0 0 24 24" aria-hidden="true">
        <g className="spinner-arcs">
          <circle className="spinner-c3" cx="12" cy="12" r="10" pathLength="60" strokeDasharray="14 46" strokeDashoffset="0" />
          <circle className="spinner-c2" cx="12" cy="12" r="10" pathLength="60" strokeDasharray="14 46" strokeDashoffset="-20" />
          <circle className="spinner-c1" cx="12" cy="12" r="10" pathLength="60" strokeDasharray="14 46" strokeDashoffset="-40" />
        </g>
        <path className="spinner-key" d={keystone} transform="translate(-1.44 2.13) scale(0.42)" />
      </svg>
      {words && <span>{words}</span>}
    </div>
  );
}
