package web

import (
	"database/sql"
	"errors"
)

// The trusted mark (P7-07). A person's database gains the column by the
// tenant migration 20261128T160000_search_provider_trusted.sql; the
// install's data/admin/search.db gains it here, when it is opened, since its
// tables are made by installSchema and not by migrations.

// installTrusted adds the trusted column to the install's search_providers
// when it does not have it yet.
func installTrusted(db *sql.DB) error {
	rows, err := db.Query("SELECT name FROM pragma_table_info('search_providers')")
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return err
		}
		if name == "trusted" {
			return nil
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	rows.Close()
	_, err = db.Exec("ALTER TABLE search_providers ADD COLUMN trusted INTEGER NOT NULL DEFAULT 0")
	return err
}

// errOwnTrusted is why a person's own provider cannot be marked trusted:
// the spec gives the mark to the admin alone.
var errOwnTrusted = errors.New("only the admin can mark a SearXNG trusted, on the install's providers; a person's own SearXNG must be on this machine or the home network")

// checkTrusted refuses the trusted mark on a person's own provider.
func (ps *Providers) checkTrusted(pr Provider) error {
	if pr.Trusted && ps.place.group != GroupInstall {
		return errOwnTrusted
	}
	return nil
}
