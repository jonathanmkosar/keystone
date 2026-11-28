package main

// The admin's router policy (P14-05): the server's part is a file of the
// turn package and the screen's words a file of the web interface, both
// described in router-policy.md. Its proof is go test ./internal/turn
// (policy_test.go), which the tests check runs.
func init() {
	componentDocs["internal/turn/policy.go"] = "docs/architecture/router-policy.md"
	componentDocs["web/app/src/policyWords.ts"] = "docs/architecture/router-policy.md"
}
