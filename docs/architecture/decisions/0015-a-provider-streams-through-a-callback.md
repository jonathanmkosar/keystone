# 0015. A provider streams through a callback

November 24, 2026 · decided in P3-03

## The question

P3-01 fixed the format of a model call: a `Request` goes in and `Event` values come out. P3-03 needs the Go interface that carries them, because the fake provider and the three adapters must all be the same thing to the code that calls them.

Go offers four shapes for a stream: a channel of events, an iterator, a reader with a `Next` method, or a function the provider calls with each event.

## The decision

A `Provider` has one method, and it takes a function.

```go
Stream(ctx context.Context, req Request, emit func(Event) error) error
```

`Stream` calls `emit` for each event, in order, on the caller's goroutine, and returns when the stream is over. It returns nil only after a `stop` event. A failed call sends an `error` event and returns that `*Error`. An error from `emit`, or a cancelled context, ends the call at once.

The reasons:

- **Nothing is left running.** A channel needs a goroutine to fill it, and a caller that stops reading leaves that goroutine blocked unless every provider guards against it. Here the provider runs inside the call, and when the call returns it is finished.
- **Stopping a turn is one mechanism.** The context is cancelled and `Stream` returns. There is no stream object to close and no second way to stop.
- **The receiver sets the pace.** A slow browser slows the provider, with no buffer to size.
- **It fits what exists.** The collector's `Add` already has the shape of `emit`, so the simplest caller is `p.Stream(ctx, req, collector.Add)`.
- **One way to fail.** A failure is always an `error` event and a returned error, so a caller that forwards the stream and a caller that only wants the result both see it.
- It is one method on plain data, with no stream type to define, close or mock.

## What it costs

- A caller cannot pull events when it likes, or wait on two streams at once, without starting a goroutine of its own. Sub-agents that run side by side will do that, each around its own call.
- Routing that falls back to another model after a failure has to decide what to do with events it has already passed on. That is true of every shape, and it belongs to P3-08.
- A provider must check the context between events, and each adapter must turn a dropped connection into an `error` event. The fake does both, and its tests show what is expected.

If code wants to range over a stream, a wrapper that turns `Stream` into an iterator is a few lines and changes nothing here.
