package seam

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

// played is a Provider that sends a fixed list of events, the way the
// interface says a provider must.
type played []Event

func (p played) Stream(ctx context.Context, _ Request, emit func(Event) error) error {
	for _, e := range p {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := emit(e); err != nil {
			return err
		}
		if e.Type == EventError {
			return e.Error
		}
	}
	return nil
}

func TestCallFoldsAStreamIntoAResponse(t *testing.T) {
	want := fullResponse()
	events, err := Events(want)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Call(context.Background(), played(events), fullRequest())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Call returned %+v, want %+v", got, want)
	}
}

func TestCallReturnsTheStreamsOwnError(t *testing.T) {
	failure := &Error{Kind: ErrRateLimited, Message: "slow down", Retryable: true}
	stream := played{
		{Type: EventStart},
		{Type: EventBlockStart, Index: 0, Block: &Block{Type: BlockText}},
		{Type: EventBlockDelta, Index: 0, Text: "The ferry "},
		{Type: EventError, Error: failure},
	}
	_, err := Call(context.Background(), stream, fullRequest())
	var got *Error
	if !errors.As(err, &got) || *got != *failure {
		t.Errorf("Call returned %v, want the stream's error", err)
	}
}

func TestCallRefusesAStreamWithNoStop(t *testing.T) {
	stream := played{
		{Type: EventStart},
		{Type: EventBlockStart, Index: 0, Block: &Block{Type: BlockText}},
		{Type: EventBlockDelta, Index: 0, Text: "The ferry "},
		{Type: EventBlockStop, Index: 0},
	}
	if _, err := Call(context.Background(), stream, fullRequest()); err == nil {
		t.Error("a stream with no stop event gave a response")
	}
}

func TestCallStopsWhenTheContextIsCancelled(t *testing.T) {
	events, err := Events(fullResponse())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Call(ctx, played(events), fullRequest()); !errors.Is(err, context.Canceled) {
		t.Errorf("Call returned %v, want the context's error", err)
	}
}
