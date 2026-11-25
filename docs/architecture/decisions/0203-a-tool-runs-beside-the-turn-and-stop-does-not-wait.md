# 0203. A tool runs beside the turn, stop does not wait for it, and every call still gets a stored result

November 25, 2026 · decided in P7-01

## The question

Stop must cancel a running tool. A tool is someone's code: it may watch its context, or it may sit in a call that does not. A reply may also hold several calls, and the stop can come while the first runs. What does the turn wait for, and what is in the record after a stop?

## The decision

**A tool runs in a goroutine of its own**, under a context made from the turn's, with `agents.tool_timeout`. `Registry.Run` waits for the first of three things: the tool returns, the turn's context ends, or the timeout passes.

**When the turn is stopped, `Run` returns at once** with the status `stopped`, whether or not the tool has noticed. A tool that watches its context ends; one that does not is left to finish, and what it returns is thrown away.

**Every call of the reply is answered.** The one that was running gets "the turn was stopped while this tool was running", and each call after it gets "the turn was stopped before this tool ran" and never starts. The results are stored as one message and streamed, and then the turn ends as `stopped`.

**The audit log records a stopped call as `ok`**, with `"status":"stopped"` in what came back, as a stopped model call is recorded: the call did what was asked of it, which was to stop.

## Why

- The person pressed stop. A stop that waits for a tool which might never return is not a stop, and the stop route only waits `chats.stop_wait` anyway.
- Storing a result for every call keeps the record one any provider takes, so the next message in the chat needs no repair, and a reload shows each step and that it was stopped.
- Not starting the later calls is what the person asked for. Running them because they were already in the reply would be work after a stop.
- The goroutine is also what makes the timeout real for a tool that ignores its context.

## What it costs

- A tool that ignores its context can go on running after the turn has ended, holding whatever it holds, until it returns. Tools that change something (after P7-02) must watch their context, and their review should check it.
- The runner holds the tenant's store open while a turn runs. A tool still running after its turn has ended may use a store the runner has closed, and should expect an error.
- Calls of one reply run one after another, not side by side. A reply with several slow calls takes their sum.
