package seam

import "context"

// Provider sends one request to a model and streams the reply back. Each
// adapter is a Provider, and so is the fake.
//
// Stream calls emit once for each event, in order, and returns when the
// stream is over. It never calls emit from another goroutine.
//
//   - A call that works ends with a stop event, and Stream returns nil.
//   - A call that fails ends with an error event, and Stream returns that
//     *Error. A reply that breaks off before its stop event is a failure
//     too, so nil always means the reply is whole.
//   - When emit returns an error, Stream sends nothing more and returns that
//     error.
//   - When ctx is cancelled, Stream sends nothing more and returns ctx's
//     error. This is how a turn is stopped.
//
// A Provider is safe to use from several goroutines at once.
type Provider interface {
	Stream(ctx context.Context, req Request, emit func(Event) error) error
}

// Call sends a request and waits for the whole reply. A failed call comes
// back as its *Error.
func Call(ctx context.Context, p Provider, req Request) (Response, error) {
	var c Collector
	if err := p.Stream(ctx, req, c.Add); err != nil {
		return Response{}, err
	}
	return c.Response()
}
