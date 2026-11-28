import { useEffect, useState, type ReactNode } from "react";
import { Link, NavLink, useLocation, useMatch } from "react-router";
import type { Chat, Person } from "./api";
import { titleOf, useChats } from "./chats";
import { ChatLinks, ChatMenu } from "./ChatLinks";
import { useNarrow } from "./PhoneSheet";
import { ChatTitle } from "./ChatTitle";
import { Icon, type IconName } from "./Icon";
import { ImportedTag } from "./Imported";
import { IncognitoButton } from "./Incognito";
import { Mark } from "./Mark";
import { ChatProjectChip, SidebarProjects } from "./Projects";
import { UsageButton } from "./UsageRing";

// Search and Chats are not rows: the magnifier in the sidebar's head opens the
// chats screen with its search box ready (the owner, November 27, 2026).
const places: { to: string; label: string; icon: IconName }[] = [
  { to: "/projects", label: "Projects", icon: "projects" },
  { to: "/artifacts", label: "Artifacts", icon: "artifacts" },
  { to: "/agents", label: "Agents", icon: "agents" },
  { to: "/scheduled", label: "Scheduled", icon: "scheduled" },
  { to: "/memory", label: "Memory", icon: "memory" },
  { to: "/operations", label: "Operations", icon: "operations" }, // P11-03
];

function initials(name: string): string {
  const words = name.trim().split(/\s+/).filter(Boolean);
  if (words.length === 0) return "?";
  if (words.length === 1) return words[0].slice(0, 2).toUpperCase();
  return (words[0][0] + words[words.length - 1][0]).toUpperCase();
}

function titleFor(path: string): string {
  if (path === "/") return "New chat";
  if (path === "/settings" || path.startsWith("/settings/")) return "Settings";
  if (path.startsWith("/projects/")) return "Projects";
  if (path === "/chats") return "Chats";
  const place = places.find((p) => p.to === path);
  return place ? place.label : "Keystone";
}

// The sidebar shows this many recent chats. The rest are on the chats screen.
const recentShown = 30;

function ChatLink({ chat }: { chat: Chat }) {
  return (
    <NavLink to={`/chats/${chat.id}`} className="nav" title={chat.title || undefined}>
      <span className={chat.title ? "nav-text" : "nav-text nav-untitled"}>{titleOf(chat)}</span>
      <ImportedTag chat={chat} />
    </NavLink>
  );
}

/** The frame every signed-in screen sits in: sidebar, header, main area. */
export function Shell({
  person,
  onSignOut,
  children,
}: {
  person: Person;
  onSignOut: () => void;
  children: ReactNode;
}) {
  const location = useLocation();
  const openChat = useMatch("/chats/:id")?.params.id;
  const { status, chats, more } = useChats();
  const starred = chats.filter((c) => c.starred);
  const recent = chats.filter((c) => !c.starred);
  const [open, setOpen] = useState(true);
  const [drawer, setDrawer] = useState(false);
  const narrow = useNarrow(); // a phone: the chat's actions are behind one ... button (decision 0590)

  // A narrow screen shows the sidebar as a drawer, and moving to another
  // screen closes it.
  useEffect(() => setDrawer(false), [location.pathname]);

  const frame = ["frame", open ? "" : "sidebar-closed", drawer ? "drawer-open" : ""].filter(Boolean).join(" ");
  // On a phone the header floats over the page (P10-07): a chat and the new
  // chat screen run beneath it, and every other page starts under it.
  const under = openChat !== undefined || location.pathname === "/";
  const main = ["main", narrow ? "is-phone" : "", narrow && under ? "is-under" : ""].filter(Boolean).join(" ");

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
          <div className="sidebar-head-end">
            <Link to="/chats" state={{ find: true }} className="icon-button search-button" aria-label="Search chats" title="Search chats">
              <Icon name="search" />
            </Link>
            <button type="button" className="icon-button close-sidebar" aria-label="Close sidebar" onClick={toggle}>
              <Icon name="sidebar" />
            </button>
          </div>
        </div>
        <Link to="/" className="new-chat">
          <span className="new-chat-plus">
            <Icon name="plus" size={16} />
          </span>
          New chat
        </Link>
        <div className="nav-group">
          {places.map((p) => (
            <NavLink key={p.to} to={p.to} end className="nav">
              <Icon name={p.icon} />
              {p.label}
            </NavLink>
          ))}
        </div>
        {starred.length > 0 && (
          <div className="nav-group">
            <div className="nav-heading">STARRED</div>
            {starred.map((c) => (
              <ChatLink key={c.id} chat={c} />
            ))}
          </div>
        )}
        <SidebarProjects />
        <div className="nav-group nav-grow">
          <div className="nav-heading">RECENTS</div>
          {recent.slice(0, recentShown).map((c) => (
            <ChatLink key={c.id} chat={c} />
          ))}
          {(recent.length > recentShown || more) && (
            <Link to="/chats" className="nav nav-more">
              All chats
            </Link>
          )}
          {status === "ready" && recent.length === 0 && <p className="nav-empty">{starred.length > 0 ? "No other chats." : "No chats yet."}</p>}
          {status === "loading" && <p className="nav-empty">Loading.</p>}
          {status === "failed" && <p className="nav-empty">The chats could not be read.</p>}
        </div>
        <div className="sidebar-foot">
          <div className="person">
            <NavLink to="/settings" className="person-link" title="Settings">
              <span className="avatar" aria-hidden="true">
                {initials(person.name)}
              </span>
              <span className="person-name">{person.name}</span>
              <Icon name="settings" size={16} />
            </NavLink>
            <button type="button" className="icon-button" aria-label="Sign out" title="Sign out" onClick={onSignOut}>
              <Icon name="signout" />
            </button>
          </div>
          {/* The theme is chosen in Settings, General, not here (the owner, November 27, 2026). */}
        </div>
      </nav>
      <button type="button" className="scrim" aria-label="Close sidebar" tabIndex={-1} onClick={() => setDrawer(false)} />
      <main className={main}>
        {narrow ? (
          // A phone (P10-07, decision 0611): no bar, but round controls
          // floating over the page, which scrolls beneath them.
          <header className="float-header">
            <button type="button" className="float-button" aria-label="Open sidebar" title="Open sidebar" onClick={toggle}>
              <Icon name="menu" size={22} />
            </button>
            <div className="float-centre">
              {openChat ? <ChatTitle id={openChat} phone /> : location.pathname !== "/" && <span className="float-pill float-title">{titleFor(location.pathname)}</span>}
            </div>
            <div className="float-end">
              {openChat && (
                <div className="float-pill float-actions">
                  {/* A turn's working mark is in its reply, not here; the ring is the month's usage (the owner, November 27, 2026). */}
                  <UsageButton chatId={openChat} />
                  <ChatMenu key={openChat} id={openChat} />
                </div>
              )}
              {location.pathname === "/" && <IncognitoButton />}
            </div>
          </header>
        ) : (
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
            {openChat ? <ChatTitle id={openChat} /> : <span className="header-title">{titleFor(location.pathname)}</span>}
            {openChat && <ChatProjectChip key={openChat} chatId={openChat} />}
          </div>
          {openChat && <ChatLinks key={openChat} id={openChat} />}
        </header>
        )}
        <div className="content">{children}</div>
      </main>
    </div>
  );
}
