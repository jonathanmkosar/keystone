import { Link } from "react-router";

// The list of every chat is the table in ManageChats.tsx (P4-06).
export { Chats } from "./ManageChats";

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
