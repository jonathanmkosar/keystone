package main

// People, the install and monitoring (P15-02): the numbers a person's page
// and the admin read one way (internal/figures), and the door a new person
// sets their password at with a setup code (internal/setupapi).
func init() {
	componentDocs["internal/figures"] = "docs/architecture/people.md"
	componentDocs["internal/setupapi"] = "docs/architecture/people.md"
	networkAllowed["internal/setupapi"] = "the setup door's handler, served by the server; it opens no connection"
	// figures is what the admin view reads a person's numbers through, so the
	// admin isolation check holds its imports as it holds the view's
	// (decision 0700).
	adminPackages = append(adminPackages, "internal/figures")
}
