package main

// Admin settings from the browser (P15-03): the install's own setting
// values in keystone.json. Their routes are admin routes, held by the
// admin isolation check like every other.
func init() {
	componentDocs["internal/installsettings"] = "docs/architecture/admin-settings.md"
}
