import { useCallback, useEffect, useState } from "react";
import { Navigate, Route, Routes, useMatch } from "react-router";
import { ArtifactPage, Artifacts } from "./ArtifactPage";
import { ApiError, listSettings, me, setSetting, signOut, type Person } from "./api";
import { Shell } from "./Shell";
import { NotReady, SignIn } from "./SignIn";
import { ChatPage, Start } from "./Chat";
import { ChatsProvider } from "./chats";
import { Chats, Missing, Soon } from "./pages";
import { Settings } from "./Settings";
import { Scheduled } from "./Scheduled";
import { Memory } from "./Memory";
import { ProjectPage, Projects } from "./Projects";
import { Operations } from "./Operations";
import { useTheme, type ThemeChoice } from "./theme";

// The setting that holds a person's theme, so it follows them to another
// browser. The browser's own copy is what the page wears before sign-in.
const themeSetting = "appearance.theme";

type Session =
  | { state: "loading" }
  | { state: "signed-in"; person: Person }
  | { state: "signed-out" }
  | { state: "not-ready"; status: number };

export function App() {
  const [session, setSession] = useState<Session>({ state: "loading" });
  const [theme, setTheme] = useTheme();
  const alone = useMatch("/artifacts/:id") !== null; // an artifact's publish link (P9-03)

  const check = useCallback(async () => {
    setSession({ state: "loading" });
    try {
      setSession({ state: "signed-in", person: await me() });
    } catch (e) {
      if (e instanceof ApiError && e.kind === "signed-out") setSession({ state: "signed-out" });
      else setSession({ state: "not-ready", status: e instanceof ApiError ? e.status : 0 });
    }
  }, []);

  useEffect(() => {
    void check();
  }, [check]);

  const leave = useCallback(async () => {
    try {
      await signOut();
    } catch {
      // Whatever the server said, ask it again who is signed in.
    }
    await check();
  }, [check]);

  // At sign-in, a theme the person chose on any browser replaces this
  // browser's. A person who never chose one keeps what this browser has.
  const signedIn = session.state === "signed-in";
  useEffect(() => {
    if (!signedIn) return;
    let live = true;
    listSettings().then(
      ({ settings }) => {
        const kept = settings.find((s) => s.key === themeSetting);
        if (live && kept?.overridden && (kept.value === "dark" || kept.value === "light" || kept.value === "system")) setTheme(kept.value);
      },
      () => {
        // The theme is a comfort. The browser's own choice stands.
      },
    );
    return () => {
      live = false;
    };
  }, [signedIn, setTheme]);

  // A choice made while signed in is worn at once and kept with the settings.
  const chooseTheme = useCallback(
    (choice: ThemeChoice) => {
      setTheme(choice);
      setSetting(themeSetting, choice).catch(() => {
        // Still worn here, and kept in this browser.
      });
    },
    [setTheme],
  );

  if (session.state === "loading") {
    return (
      <div className="center-page" aria-busy="true">
        <div className="center-card">
          <div className="brand brand-center">
            <span className="wordmark">Keystone</span>
          </div>
          <p className="quiet">Loading.</p>
        </div>
      </div>
    );
  }

  if (session.state === "not-ready") {
    return <NotReady status={session.status} theme={theme} onTheme={setTheme} onRetry={check} />;
  }

  if (session.state === "signed-out") {
    return (
      <SignIn
        theme={theme}
        onTheme={setTheme}
        onSignedIn={(person) => setSession({ state: "signed-in", person })}
        onNotReady={(status) => setSession({ state: "not-ready", status })}
      />
    );
  }

  // An artifact's publish link shows it alone, outside the frame (P9-03).
  if (alone) return <ArtifactPage />;

  return (
    <ChatsProvider onSignedOut={check}>
      <Shell person={session.person} onSignOut={leave}>
        <Routes>
          <Route path="/" element={<Start person={session.person} />} />
          <Route path="/signin" element={<Navigate to="/" replace />} />
          <Route path="/search" element={<Navigate to="/chats" replace state={{ find: true }} />} />
          <Route path="/chats" element={<Chats />} />
          <Route path="/chats/:id" element={<ChatPage />} />
          <Route path="/projects" element={<Projects />} />
          <Route path="/projects/:id" element={<ProjectPage />} />
          <Route path="/artifacts" element={<Artifacts />} />
          <Route path="/agents" element={<Soon title="Agents" />} />
          <Route path="/scheduled" element={<Scheduled />} />
          <Route path="/memory" element={<Memory />} />
          <Route path="/operations" element={<Operations onSignedOut={check} />} />
          <Route
            path="/settings/*"
            element={<Settings person={session.person} theme={theme} onTheme={chooseTheme} onThemeKept={setTheme} onSignedOut={check} />}
          />
          <Route path="*" element={<Missing />} />
        </Routes>
      </Shell>
    </ChatsProvider>
  );
}
