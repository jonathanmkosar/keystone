// /setup (P15-02): where a new person, or one an admin gave a new code,
// enters the one-time setup code and chooses their own password. The admin
// never sees or sets it. On success the server signs them in and the page
// goes to the app. Provisional: there is no mockup for it; it is drawn like
// the sign-in card. docs/architecture/admin.md.

import { useState, type FormEvent } from "react";
import { ApiError } from "./api";
import { setUp } from "./adminPeopleCalls";
import { Mark } from "./Mark";
import { ThemeSwitch } from "./ThemeSwitch";
import { useTheme } from "./theme";

/** Whether a page path is the setup page, drawn in place of the app. */
export function isSetupPath(path: string): boolean {
  return path === "/setup" || path === "/setup/";
}

export function SetupPage() {
  const [theme, setTheme] = useTheme();
  const [code, setCode] = useState("");
  const [password, setPassword] = useState("");
  const [again, setAgain] = useState("");
  const [busy, setBusy] = useState(false);
  const [message, setMessage] = useState("");
  const [done, setDone] = useState("");

  const ready = code.trim() !== "" && password !== "" && password === again && !busy;

  async function submit(e: FormEvent) {
    e.preventDefault();
    if (!ready) return;
    setBusy(true);
    setMessage("");
    try {
      const person = await setUp(code.trim(), password);
      setDone(person.name);
      window.setTimeout(() => window.location.assign("/"), 1200);
    } catch (err) {
      if (err instanceof ApiError && err.kind === "refused") setMessage("That setup code does not work. It may have been used, replaced or expired, or typed wrong: ask the admin for a new one.");
      else if (err instanceof ApiError && err.code === "disabled") setMessage("This account is disabled. Ask the admin to enable it.");
      else if (err instanceof ApiError && err.code === "bad_password") setMessage(err.said || "That password cannot be used. Choose a longer one.");
      else setMessage("Setup did not work. Check that the server is running, then try again.");
      setPassword("");
      setAgain("");
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="center-page">
      <div className="center-card">
        <div className="brand brand-center">
          <Mark size={40} />
          <span className="wordmark wordmark-large">Keystone</span>
        </div>
        <h1 className="card-title">Set up your account</h1>
        {done ? (
          <p className="quiet" role="status">
            Welcome, {done}. Your password is set and you are signed in.
          </p>
        ) : (
          <>
            <p className="quiet">Enter the setup code the admin gave you, and choose your own password. The admin never sees it. The code works once.</p>
            <form className="signin-form" onSubmit={submit}>
              <label className="field">
                <span className="field-label">Setup code</span>
                <input className="input" autoComplete="one-time-code" autoCapitalize="characters" spellCheck={false} value={code} onChange={(e) => setCode(e.target.value)} />
              </label>
              <label className="field">
                <span className="field-label">Your new password</span>
                <input className="input" type="password" autoComplete="new-password" value={password} onChange={(e) => setPassword(e.target.value)} />
              </label>
              <label className="field">
                <span className="field-label">The same password again</span>
                <input className="input" type="password" autoComplete="new-password" value={again} onChange={(e) => setAgain(e.target.value)} />
              </label>
              {password !== "" && again !== "" && password !== again && <p className="quiet">The two passwords are not the same.</p>}
              {message && (
                <p className="form-error" role="alert">
                  {message}
                </p>
              )}
              <button type="submit" className="button button-primary" disabled={!ready}>
                {busy ? "Setting up" : "Set my password"}
              </button>
            </form>
          </>
        )}
        <ThemeSwitch choice={theme} onChoose={setTheme} />
      </div>
    </div>
  );
}
