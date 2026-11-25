package approval

import (
	"errors"
	"reflect"
	"testing"
)

func TestOnlyAToolThatReadsRunsWithoutAnApproval(t *testing.T) {
	if err := Check(true, Approval{}); err != nil {
		t.Errorf("a tool that only reads was refused: %v", err)
	}
	if err := Check(false, Approval{}); !errors.Is(err, ErrNotBuilt) {
		t.Errorf("a tool that changes something, with no approval, gave %v", err)
	}
	if (Approval{}).Granted() {
		t.Error("an approval nobody gave reads as granted")
	}
	// An approval a person gave, which only this package can write.
	if err := Check(false, Approval{granted: true}); err != nil {
		t.Errorf("an approved call was refused: %v", err)
	}
}

// Nothing outside the package can make an approval that is granted: the
// type has one field, it is not exported, and no function returns the type.
func TestAnApprovalCannotBeMadeOutsideThePackage(t *testing.T) {
	typ := reflect.TypeOf(Approval{})
	for i := 0; i < typ.NumField(); i++ {
		if typ.Field(i).IsExported() {
			t.Errorf("the field %s is exported, so any package could grant an approval", typ.Field(i).Name)
		}
	}
}
