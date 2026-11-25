// The mark: three nested copper arches and an ivory keystone, from
// design/mark/. The colours are the design tokens, so the free-standing mark
// follows the theme. On light pages the mark sits on a dark tile, which is
// the answer STATE.md assumes for the open question, so the tile variant
// pins the dark theme's colours.

const dark = { c1: "#6b3a1c", c2: "#b0683a", c3: "#e8a878", key: "#f1ece0", ground: "#15191a", gap: "#0f1213" };

function Arches({ c1, c2, c3 }: { c1: string; c2: string; c3: string }) {
  return (
    <>
      <path d="M20.2 40A11.8 11.8 0 0 1 43.8 40L41 40A9 9 0 0 0 23 40Z" fill={c1} />
      <rect x="20.2" y="39.9" width="2.8" height="14.3" fill={c1} />
      <rect x="41" y="39.9" width="2.8" height="14.3" fill={c1} />
      <path d="M15.8 40A16.2 16.2 0 0 1 48.2 40L45.4 40A13.4 13.4 0 0 0 18.6 40Z" fill={c2} />
      <rect x="15.8" y="39.9" width="2.8" height="14.3" fill={c2} />
      <rect x="45.4" y="39.9" width="2.8" height="14.3" fill={c2} />
      <path d="M11.4 40A20.6 20.6 0 0 1 52.6 40L49.8 40A17.8 17.8 0 0 0 14.2 40Z" fill={c3} />
      <rect x="11.4" y="39.9" width="2.8" height="14.3" fill={c3} />
      <rect x="49.8" y="39.9" width="2.8" height="14.3" fill={c3} />
      <rect x="9.8" y="55" width="44.4" height="2.2" fill={c3} />
      <rect x="8" y="58" width="48" height="2.2" fill={c3} />
    </>
  );
}

const keystone = "M26.8 13.5A27 27 0 0 1 37.2 13.5L33.3 33.5A6.6 6.6 0 0 0 30.7 33.5Z";
const shade = "M32 13A27 27 0 0 1 37.2 13.5L33.3 33.5A6.6 6.6 0 0 0 32 33.4Z";

/** The mark. `tile` puts it on the dark tile whatever the theme. */
export function Mark({ size = 30, tile = false }: { size?: number; tile?: boolean }) {
  if (tile) {
    return (
      <svg width={size} height={size} viewBox="0 0 64 64" aria-hidden="true">
        <rect width="64" height="64" rx="14" fill={dark.ground} />
        <g transform="translate(6.4 5.6) scale(0.8)">
          <Arches c1={dark.c1} c2={dark.c2} c3={dark.c3} />
          <path d={keystone} fill={dark.key} stroke={dark.ground} strokeWidth="1.5" strokeLinejoin="round" />
          <path d={shade} fill="#000000" opacity="0.18" />
        </g>
      </svg>
    );
  }
  return (
    <svg className="mark" width={size} height={size} viewBox="0 0 64 64" aria-hidden="true">
      <g className="mark-free">
        <path className="mark-c1" d="M20.2 40A11.8 11.8 0 0 1 43.8 40L41 40A9 9 0 0 0 23 40Z" />
        <rect className="mark-c1" x="20.2" y="39.9" width="2.8" height="14.3" />
        <rect className="mark-c1" x="41" y="39.9" width="2.8" height="14.3" />
        <path className="mark-c2" d="M15.8 40A16.2 16.2 0 0 1 48.2 40L45.4 40A13.4 13.4 0 0 0 18.6 40Z" />
        <rect className="mark-c2" x="15.8" y="39.9" width="2.8" height="14.3" />
        <rect className="mark-c2" x="45.4" y="39.9" width="2.8" height="14.3" />
        <path className="mark-c3" d="M11.4 40A20.6 20.6 0 0 1 52.6 40L49.8 40A17.8 17.8 0 0 0 14.2 40Z" />
        <rect className="mark-c3" x="11.4" y="39.9" width="2.8" height="14.3" />
        <rect className="mark-c3" x="49.8" y="39.9" width="2.8" height="14.3" />
        <rect className="mark-c3" x="9.8" y="55" width="44.4" height="2.2" />
        <rect className="mark-c3" x="8" y="58" width="48" height="2.2" />
        <path className="mark-key" d={keystone} />
        <path d={shade} fill="#000000" opacity="0.18" />
      </g>
      <g className="mark-tiled">
        <rect width="64" height="64" rx="14" fill={dark.ground} />
        <g transform="translate(6.4 5.6) scale(0.8)">
          <Arches c1={dark.c1} c2={dark.c2} c3={dark.c3} />
          <path d={keystone} fill={dark.key} stroke={dark.ground} strokeWidth="1.5" strokeLinejoin="round" />
          <path d={shade} fill="#000000" opacity="0.18" />
        </g>
      </g>
    </svg>
  );
}
