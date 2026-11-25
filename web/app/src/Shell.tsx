import { useEffect, useState, type ReactNode } from "react";
import { Link, NavLink, useLocation } from "react-router";
import type { Person } from "./api";
import { Icon, type IconName } from "./Icon";
import { Mark } from "./Mark";
import { ThemeSwitch } from "./ThemeSwitch";
import type { ThemeChoice } from "./theme";

const places: { to: string; label: string; icon: IconName }[] = [
  { to: "/search", label: "Search", icon: "search" },
  { to: "/chats", label: "Chats", icon: "chats" },
  { to: "/projects", label: "Projects", icon: "projects" },
  { to: "/artifacts", label: "Artifacts", icon: "artifacts" },
  { to: "/agents", label: "Agents", icon: "agents" },
  { to: "/scheduled", label: "Scheduled", icon: "scheduled" },
];

function initials(name: string): string {
  const words = name.trim().split(/\s+/).filter(Boolean);
  if (words.length === 0) return "?";
  if (words.length === 1) return words[0].slice(0, 2).toUpperCase();
  return (words[0][0] + words[words.length - 1][0]).toUpperCase();
}

function titleFor(path: string): string {
  if (path === "/") return "New chat";
  const place = places.find((p) => p.to === path);
  return place ? place.label : "Keystone";
}

/** The frame every signed-in screen sits in: sidebar, header, main area. */
export function Shell({
  person,
  theme,
  onTheme,
  onSignOut,
  children,
}: {
  person: Person;
  theme: ThemeChoice;
  onTheme: (c: ThemeChoice) => void;
  onSignOut: () => void;
  children: ReactNode;
}) {
  const location = useLocation();
  const [open, setOpen] = useState(true);
  const [drawer, setDrawer] = useState(false);

  // A narrow screen shows the sidebar as a drawer, and moving to another
  // screen closes it.
  useEffect(() => setDrawer(false), [location.pathname]);

  const frame = ["frame", open ? "" : "sidebar-closed", drawer ? "drawer-open" : ""].filter(Boolean).join(" ");

  function toggle() {
    if (window.matchMedia("(max-width: 760px)").matches) setDrawer((d) => !d);
    else setOpen((o) => !o);
  }

  return (
    <div className={frame}>
      <nav className="sidebar" aria-label="Keystone">
        <div className="sidebar-head">
          <Link to="/" className="brand">
            <Mark size={30} />
            <span className="wordmark">Keystone</span>
          </Link>
          <button type="button" className="icon-button" aria-label="Close sidebar" onClick={toggle}>
            <Icon name="sidebar" />
          </button>
        </div>
        <Link to="/" className="new-chat">
          <span className="new-chat-plus">
            <Icon name="plus" size={16} />
          </span>
          New chat
        </Link>
        <div className="nav-group">
          {places.map((p) => (
            <NavLink key={p.to} to={p.to} className="nav">
              <Icon name={p.icon} />
              {p.label}
            </NavLink>
          ))}
        </div>
        <div className="nav-group nav-grow">
          <div className="nav-heading">RECENTS</div>
          <p className="nav-empty">No chats yet.</p>
        </div>
        <div className="sidebar-foot">
          <div className="person">
            <span className="avatar" aria-hidden="true">
              {initials(person.name)}
            </span>
            <span className="person-name">{person.name}</span>
            <button type="button" className="icon-button" aria-label="Sign out" title="Sign out" onClick={onSignOut}>
              <Icon name="signout" />
            </button>
          </div>
          <ThemeSwitch choice={theme} onChoose={onTheme} />
        </div>
      </nav>
      <button type="button" className="scrim" aria-label="Close sidebar" tabIndex={-1} onClick={() => setDrawer(false)} />
      <main className="main">
        <header className="header">
          <div className="header-left">
            <button
              type="button"
              className="icon-button header-open"
              aria-label="Open sidebar"
              onClick={toggle}
            >
              <Icon name="sidebar" />
            </button>
            <span className="header-title">{titleFor(location.pathname)}</span>
          </div>
        </header>
        <div className="content">{children}</div>
      </main>
    </div>
  );
}
