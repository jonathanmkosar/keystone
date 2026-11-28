// The admin account's pages (P15-01): /admin/sign-in, and /admin, which
// shows only admin controls. An admin is a separate account: it has no
// chats, no files and no models, and signs in apart from any person. The
// admin screens for people, the install and settings come with the items
// that build them; until then the page says who is signed in and signs out.
// docs/architecture/admin.md.

import { useEffect, useState, type FormEvent } from "react";
import { ApiError } from "./api";
import { adminSignIn, adminSignOut, adminWho, type AdminSession } from "./adminCalls";
import { Icon } from "./Icon";
import { Mark } from "./Mark";
import { ThemeSwitch } from "./ThemeSwitch";
import { useTheme } from "./theme";

/** Whether a page path is the admin account's, drawn by AdminApp in place of the person's app. */
export function isAdminPath(path: string): boolean {
  return path === "/admin" || path.startsWith("/admin/");
}

type State = { kind: "loading" } | { kind: "signed-out" } | { kind: "signed-in"; session: AdminSession } | { kind: "not-ready"; status: number };

function go(path: string) {
  if (window.location.pathname !== path) window.history.replaceState(null, "", path);
}

function Card({ children }: { children: React.ReactNode }) {
  return (
    <div className="center-page">
      <div className="center-card">
        <div className="brand brand-center">
          <Mark size={40} />
          <span className="wordmark wordmark-large">Keystone</span>
        </div>
        {children}
      </div>
    </div>
  );
}

function when(iso: string): string {
  const d = new Date(iso);
  return Number.isNaN(d.getTime()) ? iso : d.toLocaleString();
}

export function AdminApp() {
  const [state, setState] = useState<State>({ kind: "loading" });
  const [theme, setTheme] = useTheme();

  async function check() {
    setState({ kind: "loading" });
    try {
      const session = await adminWho();
      setState({ kind: "signed-in", session });
      go("/admin");
    } catch (e) {
      if (e instanceof ApiError && e.kind === "signed-out") {
        setState({ kind: "signed-out" });
        go("/admin/sign-in");
      } else setState({ kind: "not-ready", status: e instanceof ApiError ? e.status : 0 });
    }
  }

  useEffect(() => {
    void check();
  }, []);

  if (state.kind === "loading") {
    return (
      <Card>
        <p className="quiet" aria-busy="true">
          Loading.
        </p>
      </Card>
    );
  }
  if (state.kind === "not-ready") {
    return (
      <Card>
        <h1 className="card-title">The server is not ready</h1>
        <p className="quiet">Keystone did not answer{state.status ? ` (${state.status})` : ""}. Start it with keystone serve, then try again.</p>
        <button type="button" className="button button-primary" onClick={() => void check()}>
          <Icon name="retry" size={16} />
          Try again
        </button>
        <ThemeSwitch choice={theme} onChoose={setTheme} />
      </Card>
    );
  }
  if (state.kind === "signed-out") {
    return (
      <AdminSignIn
        onSignedIn={(session) => {
          setState({ kind: "signed-in", session });
          go("/admin");
        }}
        footer={<ThemeSwitch choice={theme} onChoose={setTheme} />}
      />
    );
  }

  const { account, session } = state.session;
  async function leave() {
    try {
      await adminSignOut();
    } catch {
      // Whatever the server said, ask it again who is signed in.
    }
    await check();
  }
  return (
    <Card>
      <h1 className="card-title">Admin</h1>
      <p className="quiet">
        Signed in as <strong>{account.name}</strong>, an admin account. It shows admin controls only: it has no chats, no files and no models, and it sees people's numbers and settings, never
        their data or their logs. Each time it reads something of a person's, that person can see it in their own Settings.
      </p>
      <p className="quiet">
        This session ends at {when(session.expires)}, or sooner if it is left unused until {when(session.idle_until)}.
      </p>
      <p className="quiet">The admin screens for people, the install and settings are not built yet.</p>
      <button type="button" className="button" onClick={() => void leave()}>
        Sign out
      </button>
      <ThemeSwitch choice={theme} onChoose={setTheme} />
    </Card>
  );
}

/** /admin/sign-in: the admin account's name and its password. */
function AdminSignIn({ onSignedIn, footer }: { onSignedIn: (s: AdminSession) => void; footer: React.ReactNode }) {
  const [account, setAccount] = useState("");
  const [password, setPassword] = useState("");
  const [busy, setBusy] = useState(false);
  const [message, setMessage] = useState("");

  async function submit(e: FormEvent) {
    e.preventDefault();
    if (!account.trim() || !password || busy) return;
    setBusy(true);
    setMessage("");
    try {
      onSignedIn(await adminSignIn(account.trim(), password));
    } catch (err) {
      if (err instanceof ApiError && err.kind === "refused") setMessage("That account name or password is not right.");
      else if (err instanceof ApiError && err.kind === "wait") setMessage("Too many wrong passwords. Wait a minute, then try again.");
      else setMessage("Sign-in did not work. Check that the server is running, then try again.");
      setPassword("");
    } finally {
      setBusy(false);
    }
  }

  return (
    <Card>
      <h1 className="card-title">Admin sign-in</h1>
      <p className="quiet">The admin account is separate from your own. An account is made on the server with go run ./tools/admin.</p>
      <form className="signin-form" onSubmit={submit}>
        <label className="field">
          <span className="field-label">Admin account</span>
          <input className="input" autoComplete="username" value={account} onChange={(e) => setAccount(e.target.value)} />
        </label>
        <label className="field">
          <span className="field-label">Password</span>
          <input className="input" type="password" autoComplete="current-password" value={password} onChange={(e) => setPassword(e.target.value)} />
        </label>
        {message && (
          <p className="form-error" role="alert">
            {message}
          </p>
        )}
        <button type="submit" className="button button-primary" disabled={!account.trim() || !password || busy}>
          {busy ? "Signing in" : "Sign in"}
        </button>
      </form>
      {footer}
    </Card>
  );
}
