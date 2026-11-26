package attach

import (
	"context"
	"errors"

	"github.com/jonathanmkosar/keystone/internal/recap"
	"github.com/jonathanmkosar/keystone/internal/tenant"
)

// Answer is what may become of one attached chat in one model call.
type Answer struct {
	// Show says the chat may reach the call: its title, its recap and its
	// messages.
	Show bool
	// Sensitive says it holds content from a sensitive space. When it is
	// shown, the chat it goes into must be held first (recall.Hold), since
	// what a call is given stays in that chat and is sent again with every
	// later call of it.
	Sensitive bool
	// Incognito says it is incognito, so no call carries it.
	Incognito bool
}

// MayShow is fixed rule 3's answer for one attached chat, and the incognito
// rule's. mayReadSensitive is the answer for the least of the models the
// call's route may reach. A chat that holds sensitive content is shown only
// when every one of them may read it, as recap.MayShow says for a past
// chat. A mark or a flag that cannot be read counts against showing: the
// answer is then the safe one, and the error says what could not be read.
func MayShow(ctx context.Context, s *tenant.Store, chatID string, mayReadSensitive bool) (Answer, error) {
	incognito, err1 := Incognito(ctx, s, chatID)
	sensitive, err2 := recap.Sensitive(ctx, s, chatID) // true when it cannot be read
	a := Answer{Sensitive: sensitive, Incognito: incognito}
	a.Show = !incognito && recap.MayShow(sensitive, mayReadSensitive)
	return a, errors.Join(err1, err2)
}
