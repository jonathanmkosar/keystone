// The publish link (P9-03): /artifacts/<id> shows the artifact alone, with
// its versions, outside the frame of the app. The page reads it through the
// API with the person's session, so it opens only for the person who owns
// it, signed in; anyone else is shown sign-in, and another person on the
// same Keystone is told it is not here (decision 0423). And the Artifacts
// screen, /artifacts, which lists every artifact of the person.

import { useEffect, useState } from "react";
import { Link, useMatch, useSearchParams } from "react-router";
import { ApiError } from "./api";
import { ArtifactBody } from "./ArtifactPanel";
import { allArtifacts, typeLabel, when, type Artifact } from "./artifacts";
import { Icon } from "./Icon";
import { Mark } from "./Mark";
import "./artifacts.css";

/** The artifact at /artifacts/<id>, alone. ?version= opens one version. */
export function ArtifactPage() {
  // The page is drawn outside the app's routes, so it reads its own address.
  const id = useMatch("/artifacts/:id")?.params.id ?? "";
  const [query] = useSearchParams();
  const asked = Number(query.get("version"));
  return (
    <div className="artifact-page">
      <header className="artifact-page-head">
        <Link to="/" className="artifact-page-brand" aria-label="Keystone">
          <Mark size={22} />
          <span className="wordmark">Keystone</span>
        </Link>
        <span className="artifact-page-note">Only you can open this page, signed in to this Keystone.</span>
        <Link to="/artifacts" className="copy-button">
          <Icon name="artifacts" size={16} />
          Your artifacts
        </Link>
      </header>
      <main className="artifact-page-body">
        <ArtifactBody key={id} artifactId={id} version={Number.isInteger(asked) && asked > 0 ? asked : undefined} />
      </main>
    </div>
  );
}

type Listed = { state: "loading" } | { state: "ready"; list: Artifact[] } | { state: "failed" };

/** Every artifact of the person, the one changed last first. */
export function Artifacts() {
  const [listed, setListed] = useState<Listed>({ state: "loading" });
  useEffect(() => {
    let live = true;
    allArtifacts().then(
      (list) => live && setListed({ state: "ready", list }),
      (e: unknown) => live && setListed({ state: e instanceof ApiError && e.kind === "signed-out" ? "loading" : "failed" }),
    );
    return () => {
      live = false;
    };
  }, []);
  return (
    <div className="list-page">
      <div className="list-head">
        <h1 className="page-title">Artifacts</h1>
      </div>
      {listed.state === "loading" && (
        <p className="quiet" aria-busy="true">
          Loading.
        </p>
      )}
      {listed.state === "failed" && <p className="quiet">The artifacts could not be read. Check that the server is running.</p>}
      {listed.state === "ready" && listed.list.length === 0 && <p className="quiet">No artifacts yet. Ask for a page, a diagram, a component or a document in a chat, and it opens beside it.</p>}
      {listed.state === "ready" && listed.list.length > 0 && (
        <section className="list-section" aria-label="Artifacts">
          {listed.list.map((a) => (
            <div key={a.id} className="list-row artifact-row">
              <Link to={a.link} className="list-title">
                {a.title}
              </Link>
              <span className="artifact-row-kind">{`${typeLabel(a.type, a.language)} · version ${a.latest}`}</span>
              {a.sensitive && <span className="artifact-pill is-sensitive">sensitive</span>}
              <Link to={`/chats/${a.chat_id}`} className="artifact-row-chat">
                its chat
              </Link>
              <span className="list-when">{when(a.updated)}</span>
            </div>
          ))}
        </section>
      )}
    </div>
  );
}
