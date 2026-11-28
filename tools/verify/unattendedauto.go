package main

// Auto for unattended work (P14-06): sub-agents, background tasks and
// scheduled runs start on Auto unless the call that started them names a
// model. Its files are listed with their doc here; its proof is in the
// packages' own tests (docs/architecture/unattended-auto.md, "Checked by").
func init() {
	componentDocs["internal/turn/unattendedauto.go"] = "docs/architecture/unattended-auto.md"
	componentDocs["internal/jobs/auto.go"] = "docs/architecture/unattended-auto.md"
	componentDocs["internal/subagents/auto.go"] = "docs/architecture/unattended-auto.md"
}
