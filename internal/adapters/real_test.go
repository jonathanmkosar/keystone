package adapters

import (
	"testing"

	"github.com/jonathanmkosar/keystone/internal/models"
	"github.com/jonathanmkosar/keystone/settings"
)

func TestRealHoldsTheThreeAdaptersAndNoFake(t *testing.T) {
	reg, err := settings.Registry()
	if err != nil {
		t.Fatal(err)
	}
	set, err := Real(reg)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{models.AdapterAnthropic, models.AdapterResponses, models.AdapterChatCompletions} {
		if set[name] == nil {
			t.Errorf("the set has no builder for %s", name)
		}
	}
	if set[models.AdapterFake] != nil {
		t.Error("the set a server starts with holds the fake")
	}
}
