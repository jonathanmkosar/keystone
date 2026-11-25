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
} as const;

export type IconName = keyof typeof shapes;

export function Icon({ name, size = 20 }: { name: IconName; size?: number }) {
  return (
    <svg className="ico" width={size} height={size} viewBox="0 0 24 24" aria-hidden="true">
      {shapes[name]}
    </svg>
  );
}
