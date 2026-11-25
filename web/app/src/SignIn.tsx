import { useEffect, useState, type FormEvent } from "react";
import { ApiError, listPeople, signIn, type Person } from "./api";
import { Icon } from "./Icon";
import { Mark } from "./Mark";
import { ThemeSwitch } from "./ThemeSwitch";
import type { ThemeChoice } from "./theme";

type People = { state: "loading" } | { state: "ready"; people: Person[] } | { state: "not-ready"; status: number };

function initials(name: string): string {
  const words = name.trim().split(/s+/).filter(Boolean);
  if (words.length === 0) return "?";
  if (words.length === 1) return words[0].slice(0, 2).toUpperCase();
  return (words[0][0] + words[words.length - 1][0]).toUpperCase();
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

/** Shown while the server is checked, and when it does not answer. */
export function NotReady({
  status,
  theme,
  onTheme,
  onRetry,
}: {
  status: number;
  theme: ThemeChoice;
  onTheme: (c: ThemeChoice) => void;
  onRetry: () => void;
}) {
  return (
    <Card>
      <h1 className="card-title">The server is not ready</h1>
      <p className="quiet">
        The page loaded, but Keystone did not answer the interface's calls
        {status ? ` (${status})` : ""}. Start it with keystone serve, or wait for the server to finish starting.
      </p>
      <button type="button" className="button button-primary" onClick={onRetry}>
        <Icon name="retry" size={16} />
        Try again
      </button>
      <ThemeSwitch choice={theme} onChoose={onTheme} />
    </Card>
  );
}

/** Lists the people on the install. The person picks their own and types a password. */
export function SignIn({
  theme,
  onTheme,
  onSignedIn,
  onNotReady,
}: {
  theme: ThemeChoice;
  onTheme: (c: ThemeChoice) => void;
  onSignedIn: (p: Person) => void;
  onNotReady: (status: number) => void;
}) {
  const [people, setPeople] = useState<People>({ state: "loading" });
  const [chosen, setChosen] = useState<string>("");
  const [password, setPassword] = useState("");
  const [busy, setBusy] = useState(false);
  const [message, setMessage] = useState("");

  async function load() {
    setPeople({ state: "loading" });
    try {
      const list = await listPeople();
      setPeople({ state: "ready", people: list });
      if (list.length === 1) setChosen(list[0].id);
    } catch (e) {
      setPeople({ state: "not-ready", status: e instanceof ApiError ? e.status : 0 });
    }
  }

  useEffect(() => {
    void load();
  }, []);

  async function submit(e: FormEvent) {
    e.preventDefault();
    if (!chosen || !password || busy) return;
    setBusy(true);
    setMessage("");
    try {
      onSignedIn(await signIn(chosen, password));
    } catch (err) {
      if (err instanceof ApiError && err.kind === "refused") setMessage("That password is not right.");
      else if (err instanceof ApiError && err.kind === "wait")
        setMessage("Too many wrong passwords. Wait a minute, then try again.");
      else if (err instanceof ApiError && err.kind === "not-ready") onNotReady(err.status);
      else setMessage("Sign-in did not work. Try again.");
      setPassword("");
    } finally {
      setBusy(false);
    }
  }

  if (people.state === "loading") {
    return (
      <Card>
        <p className="quiet" aria-busy="true">
          Loading.
        </p>
      </Card>
    );
  }
  if (people.state === "not-ready") {
    return <NotReady status={people.status} theme={theme} onTheme={onTheme} onRetry={load} />;
  }
  if (people.people.length === 0) {
    return (
      <Card>
        <h1 className="card-title">No one has been added yet</h1>
        <p className="quiet">
          This install has no people. Add one with go run ./tools/tenant, give them a password, then try again.
        </p>
        <button type="button" className="button button-primary" onClick={load}>
          <Icon name="retry" size={16} />
          Try again
        </button>
        <ThemeSwitch choice={theme} onChoose={onTheme} />
      </Card>
    );
  }

  const person = people.people.find((p) => p.id === chosen);
  return (
    <Card>
      <h1 className="card-title">Sign in</h1>
      <form className="signin-form" onSubmit={submit}>
        <div className="people" role="radiogroup" aria-label="Who is signing in">
          {people.people.map((p) => (
            <button
              key={p.id}
              type="button"
              role="radio"
              aria-checked={chosen === p.id}
              className={chosen === p.id ? "person-option is-on" : "person-option"}
              onClick={() => {
                setChosen(p.id);
                setMessage("");
              }}
            >
              <span className="avatar" aria-hidden="true">
                {initials(p.name)}
              </span>
              <span className="person-name">{p.name}</span>
            </button>
          ))}
        </div>
        <label className="field">
          <span className="field-label">Password{person ? ` for ${person.name}` : ""}</span>
          <input
            className="input"
            type="password"
            autoComplete="current-password"
            value={password}
            disabled={!chosen}
            onChange={(e) => setPassword(e.target.value)}
          />
        </label>
        {message && (
          <p className="form-error" role="alert">
            {message}
          </p>
        )}
        <button type="submit" className="button button-primary" disabled={!chosen || !password || busy}>
          {busy ? "Signing in" : "Sign in"}
        </button>
      </form>
      <ThemeSwitch choice={theme} onChoose={onTheme} />
    </Card>
  );
}
