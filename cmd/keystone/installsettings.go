package main

import (
	"flag"

	"github.com/jonathanmkosar/keystone/settings"
)

// The install's own setting values (P15-03) reach the server through the
// registry once internal/installsettings has read keystone.json, which
// openAPI does before any other setting is read.

// flagGiven reports whether a flag was given on the command line.
func flagGiven(fl *flag.FlagSet, name string) bool {
	given := false
	fl.Visit(func(f *flag.Flag) {
		if f.Name == name {
			given = true
		}
	})
	return given
}

// installedSetting returns a setting's value in force: the install's own,
// or the shipped default.
func installedSetting(key string) (string, error) {
	reg, err := settings.Registry()
	if err != nil {
		return "", err
	}
	return setting(reg, key)
}
