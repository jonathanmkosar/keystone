import { useCallback, useEffect, useState } from "react";
import { Navigate, Route, Routes } from "react-router";
import { ApiError, me, signOut, type Person } from "./api";
import { Shell } from "./Shell";
import { NotReady, SignIn } from "./SignIn";
import { Home, Missing, Soon } from "./pages";
import { useTheme } from "./theme";

type Session =
  | { state: "loading" }
  | { state: "signed-in"; person: Person }
  | { state: "signed-out" }
  | { state: "not-ready"; status: number };

export function App() {
  const [session, setSession] = useState<Session>({ state: "loading" });
  const [theme, setTheme] = useTheme();

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

  return (
    <Shell person={session.person} theme={theme} onTheme={setTheme} onSignOut={leave}>
      <Routes>
        <Route path="/" element={<Home person={session.person} />} />
        <Route path="/signin" element={<Navigate to="/" replace />} />
        <Route path="/search" element={<Soon title="Search" />} />
        <Route path="/chats" element={<Soon title="Chats" />} />
        <Route path="/projects" element={<Soon title="Projects" />} />
        <Route path="/artifacts" element={<Soon title="Artifacts" />} />
        <Route path="/agents" element={<Soon title="Agents" />} />
        <Route path="/scheduled" element={<Soon title="Scheduled" />} />
        <Route path="*" element={<Missing />} />
      </Routes>
    </Shell>
  );
}
