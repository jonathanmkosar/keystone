package schedules

// The errors another package meets when it calls the scheduler, by names it
// can test with errors.Is (P8-06: internal/wikijobs runs its jobs through
// schedules of their own).
var (
	// ErrNotFound: there is no such schedule in this person's record.
	ErrNotFound = errNoSchedule
	// ErrInvalid: a schedule's body does not fit.
	ErrInvalid = errInvalid
	// ErrRunOpen: a run of the schedule is under way.
	ErrRunOpen = errRunOpen
)
