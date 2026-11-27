package main

// The admin mark and step-up (P15-01): the admin view and the admin routes.
func init() {
	componentDocs["internal/adminview"] = "docs/architecture/admin.md"
	componentDocs["internal/adminapi"] = "docs/architecture/admin.md"
	networkAllowed["internal/adminapi"] = "the admin routes' handlers, served by the server; it opens no connection"
}
