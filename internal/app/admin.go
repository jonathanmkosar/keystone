package app

import (
	"errors"

	"github.com/jonathanmkosar/keystone/internal/adminaccount"
)

// openAdmin opens the admin database, data/admin/admin.db (P15-01), for the
// admin's sign-in doors. It is called for a request, never at start, so the
// OpenAPI document, which has no data folder, needs none.
func (c Config) openAdmin() (*adminaccount.Store, error) {
	if c.Install == nil {
		return nil, errors.New("app: no data folder was given")
	}
	return adminaccount.Open(c.Install.Dir())
}
