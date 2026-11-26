// Line icons, drawn as in the mockups: 24 by 24, stroked in the current
// text colour.

const shapes = {
  sidebar: (
    <>
      <rect x="3" y="4" width="18" height="16" rx="3" />
      <path d="M9 4v16" />
    </>
  ),
  plus: <path d="M12 5v14M5 12h14" />,
  search: (
    <>
      <circle cx="11" cy="11" r="6" />
      <path d="m20 20-4.5-4.5" />
    </>
  ),
  web: (
    <>
      <circle cx="12" cy="12" r="8" />
      <path d="M4 12h16M12 4c-2.5 2.2-3.5 5-3.5 8s1 5.8 3.5 8c2.5-2.2 3.5-5 3.5-8s-1-5.8-3.5-8" />
    </>
  ),
  chats: <path d="M5 5h14v10H9l-4 4z" />,
  projects: <path d="M3 7h7l2 2h9v10H3z" />,
  artifacts: (
    <>
      <rect x="4" y="4" width="10" height="10" rx="2" />
      <path d="M10 20h8a2 2 0 0 0 2-2v-8" />
    </>
  ),
  agents: <path d="M4 18h16M7 14h10M10 10h4" />,
  scheduled: (
    <>
      <circle cx="12" cy="12" r="8" />
      <path d="M12 8v4l3 2" />
    </>
  ),
  chevron: <path d="m6 9 6 6 6-6" />,
  sun: (
    <>
      <circle cx="12" cy="12" r="4" />
      <path d="M12 3v2M12 19v2M3 12h2M19 12h2M5.6 5.6l1.4 1.4M17 17l1.4 1.4M5.6 18.4 7 17M17 7l1.4-1.4" />
    </>
  ),
  moon: <path d="M20 14.5A8 8 0 0 1 9.5 4a8 8 0 1 0 10.5 10.5z" />,
  system: (
    <>
      <rect x="3" y="5" width="18" height="12" rx="2" />
      <path d="M9 21h6M12 17v4" />
    </>
  ),
  signout: (
    <>
      <path d="M10 4H6a2 2 0 0 0-2 2v12a2 2 0 0 0 2 2h4" />
      <path d="M15 8l4 4-4 4M19 12H9" />
    </>
  ),
  retry: (
    <>
      <path d="M4 12a8 8 0 0 1 14-5.3L20 9" />
      <path d="M20 4v5h-5" />
      <path d="M20 12a8 8 0 0 1-14 5.3L4 15" />
      <path d="M4 20v-5h5" />
    </>
  ),
  settings: (
    <>
      <path d="M4 7h9M17 7h3M4 17h3M11 17h9" />
      <circle cx="15" cy="7" r="2" />
      <circle cx="9" cy="17" r="2" />
    </>
  ),
  send: <path d="M12 19V5M6 11l6-6 6 6" />,
  stop: <rect x="7" y="7" width="10" height="10" rx="2" />,
  copy: (
    <>
      <rect x="8" y="8" width="12" height="12" rx="2" />
      <path d="M16 8V5a1 1 0 0 0-1-1H5a1 1 0 0 0-1 1v10a1 1 0 0 0 1 1h3" />
    </>
  ),
  check: <path d="m5 12.5 4.5 4.5L19 7.5" />,
  caret: <path d="m9 6 6 6-6 6" />,
  star: <path d="m12 4 2.5 5.1 5.5.8-4 3.9.9 5.6-4.9-2.6-4.9 2.6.9-5.6-4-3.9 5.5-.8z" />,
  pencil: (
    <>
      <path d="M4 20h4L19 9l-4-4L4 16z" />
      <path d="m13 7 4 4" />
    </>
  ),
  trash: (
    <>
      <path d="M4 7h16M10 4h4M6 7l1 13h10l1-13" />
      <path d="M10 11v5M14 11v5" />
    </>
  ),
  tool: <path d="M5 19h14M7 15h10M9.5 11h5" />,
  // A connector (P7-04): a plug.
  plug: <path d="M9 3v5M15 3v5M6 8h12v3a6 6 0 0 1-12 0zM12 17v4" />,
  // Gmail and Calendar's steps (P12-02).
  mail: (
    <>
      <rect x="3" y="5" width="18" height="14" rx="2" />
      <path d="m4 7 8 6 8-6" />
    </>
  ),
  calendar: (
    <>
      <rect x="4" y="5" width="16" height="15" rx="2" />
      <path d="M4 10h16M9 3v4M15 3v4" />
    </>
  ),
  alert: (
    <>
      <path d="M12 4 3 19h18z" />
      <path d="M12 10v4M12 16.5v.5" />
    </>
  ),
  // The turn inspector's, as in the chat header of MainDark.html (P5-04).
  layers: (
    <>
      <path d="m12 3 9 5-9 5-9-5z" />
      <path d="m3 13 9 5 9-5" />
    </>
  ),
  pin: (
    <>
      <path d="M9 4h6l-1 6 3 3H7l3-3z" />
      <path d="M12 13v7" />
    </>
  ),
  close: <path d="M6 6l12 12M18 6 6 18" />,
  // An attached chat (P5-06): a chat's bubble with a link.
  chatlink: (
    <>
      <path d="M4 5h12v8H8l-4 4z" />
      <path d="M14 17h2a2.5 2.5 0 0 0 0-5M20 17h-2a2.5 2.5 0 0 1 0-5" />
    </>
  ),
  // A handoff (P5-06): out of one chat and on into the next.
  handoff: (
    <>
      <path d="M4 5h9v6M4 5v9h6" />
      <path d="M13 15h7M17 12l3 3-3 3" />
    </>
  ),
  // The Memory screen's (P6-07): a page with a corner turned down.
  memory: (
    <>
      <path d="M6 3h9l3 3v15H6z" />
      <path d="M9 10h6M9 14h6M9 18h4" />
    </>
  ),
  // An incognito chat's (P6-07): an eye with a line through it.
  incognito: (
    <>
      <path d="M3 12s3.5-6 9-6 9 6 9 6-3.5 6-9 6-9-6-9-6z" />
      <path d="M4 4l16 16" />
    </>
  ),
} as const;

export type IconName = keyof typeof shapes;

export function Icon({ name, size = 20 }: { name: IconName; size?: number }) {
  return (
    <svg className="ico" width={size} height={size} viewBox="0 0 24 24" aria-hidden="true">
      {shapes[name]}
    </svg>
  );
}
