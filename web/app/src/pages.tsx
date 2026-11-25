import { Link } from "react-router";
import type { Person } from "./api";
import { Mark } from "./Mark";

function greeting(): string {
  const hour = new Date().getHours();
  if (hour < 12) return "Good morning";
  if (hour < 18) return "Good afternoon";
  return "Good evening";
}

/** The first screen after sign-in. The composer arrives with the chat screen (P4-04). */
export function Home({ person }: { person: Person }) {
  return (
    <div className="home">
      <Mark size={56} />
      <h1 className="home-greeting">
        {greeting()}, {person.name}
      </h1>
      <p className="quiet">Chat is not built yet. This is the shell it will sit in.</p>
    </div>
  );
}

/** A screen the sidebar links to that a later work item builds. */
export function Soon({ title }: { title: string }) {
  return (
    <div className="home">
      <h1 className="page-title">{title}</h1>
      <p className="quiet">This screen is not built yet.</p>
    </div>
  );
}

export function Missing() {
  return (
    <div className="home">
      <h1 className="page-title">Page not found</h1>
      <p className="quiet">Nothing lives at this address.</p>
      <Link to="/" className="button">
        Back to the start
      </Link>
    </div>
  );
}
