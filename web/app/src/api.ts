// Every call the web interface makes to the Keystone server is in this file.
//
// The routes are the API shell's (P2-05), under /api/v1/. They are described
// in docs/architecture/api.md and docs/api/openapi.json.
//
// The session is a cookie the server sets and the page cannot read, so no
// token is ever held here.

const paths = {
  people: "/api/v1/signin/people",
  session: "/api/v1/session",
};

export type Person = { id: string; name: string };

export type ApiErrorKind =
  | "not-ready" // the API is not there: a 404, a 405, a non-JSON answer, or no connection
  | "refused" // wrong password
  | "wait" // too many wrong passwords; try again later
  | "signed-out" // no session
  | "failed"; // anything else

export class ApiError extends Error {
  kind: ApiErrorKind;
  status: number;
  constructor(kind: ApiErrorKind, status: number) {
    super(`api: ${kind} (${status})`);
    this.name = "ApiError";
    this.kind = kind;
    this.status = status;
  }
}

function kindOf(status: number): ApiErrorKind {
  if (status === 404 || status === 405) return "not-ready";
  if (status === 401) return "signed-out";
  if (status === 429) return "wait";
  return "failed";
}

async function call(method: "GET" | "POST" | "DELETE", path: string, body?: unknown): Promise<unknown> {
  let res: Response;
  try {
    res = await fetch(path, {
      method,
      credentials: "same-origin",
      headers: body === undefined ? { Accept: "application/json" } : { Accept: "application/json", "Content-Type": "application/json" },
      body: body === undefined ? undefined : JSON.stringify(body),
    });
  } catch {
    throw new ApiError("not-ready", 0);
  }
  if (res.status === 204) return undefined;
  if (!res.ok) throw new ApiError(kindOf(res.status), res.status);
  const type = res.headers.get("Content-Type") ?? "";
  if (!type.includes("application/json")) throw new ApiError("not-ready", res.status);
  try {
    return await res.json();
  } catch {
    throw new ApiError("not-ready", res.status);
  }
}

function isPerson(v: unknown): v is Person {
  if (typeof v !== "object" || v === null) return false;
  const p = v as Record<string, unknown>;
  return typeof p.id === "string" && typeof p.name === "string";
}

function personFrom(data: unknown): Person {
  const person = (data as { person?: unknown } | undefined)?.person;
  if (!isPerson(person)) throw new ApiError("not-ready", 200);
  return person;
}

/** The people on this install who can sign in. Needs no session. */
export async function listPeople(): Promise<Person[]> {
  const data = (await call("GET", paths.people)) as { people?: unknown } | undefined;
  const people = data?.people;
  if (!Array.isArray(people) || !people.every(isPerson)) throw new ApiError("not-ready", 200);
  return people;
}

/** Signs one person in with their password. The server sets the session cookie. */
export async function signIn(person: string, password: string): Promise<Person> {
  try {
    return personFrom(await call("POST", paths.session, { tenant: person, password }));
  } catch (e) {
    // At the door a 401 means the password was refused, not that a session ended.
    if (e instanceof ApiError && e.status === 401) throw new ApiError("refused", 401);
    throw e;
  }
}

/** Ends the session. */
export async function signOut(): Promise<void> {
  await call("DELETE", paths.session);
}

/** The signed-in person. Throws ApiError "signed-out" when there is no session. */
export async function me(): Promise<Person> {
  return personFrom(await call("GET", paths.session));
}
