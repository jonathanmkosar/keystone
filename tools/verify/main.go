// Command verify runs the standing checks. Every check must pass before a
// merge to main. It needs no network and no keys.
//
//	go run ./tools/verify
package main

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/jonathanmkosar/keystone/internal/project"
	"github.com/jonathanmkosar/keystone/settings"
)

type check struct {
	name string
	run  func(root string) error
}

// componentDocs maps each part of the code to the doc that describes it.
// Adding a part without adding its doc fails the docs check.
var componentDocs = map[string]string{
	"cmd/keystone":     "docs/architecture/cli.md",
	"internal/tenant":  "docs/architecture/tenants.md",
	"tools/tenant":     "docs/architecture/tools.md",
	"settings":         "docs/architecture/settings.md",
	"internal/project": "docs/architecture/project.md",
	"internal/seam":    "docs/architecture/seam.md",
	"internal/chat":    "docs/architecture/chat.md",
	"internal/signin":  "docs/architecture/signin.md",
	"internal/secrets": "docs/architecture/secrets.md",
	"tools/verify":     "docs/architecture/tools.md",
	"tools/doctor":     "docs/architecture/tools.md",
	"tools/snapshot":   "docs/architecture/tools.md",
	"tools/rebuild":    "docs/architecture/tools.md",
	"web":              "docs/architecture/web.md",

	"internal/knowledge":               "docs/architecture/knowledge.md",
	"internal/knowledge/knowledgetest": "docs/architecture/knowledge.md",
	"internal/adapters/fake":           "docs/architecture/fake-provider.md",
	"internal/audit":                   "docs/architecture/audit.md",

	"internal/tenantsettings": "docs/architecture/tenant-settings.md",

	"internal/knowledge/builtin": "docs/architecture/knowledge-builtin.md",
	"internal/models":            "docs/architecture/models.md",
	"internal/adapters":          "docs/architecture/models.md",
	"internal/testtenant":        "docs/architecture/test-tenant.md",
	"fixtures/test-tenant":       "docs/architecture/test-tenant.md",
	"internal/api":               "docs/architecture/api.md",
	"internal/app":               "docs/architecture/api.md",
	"internal/budget":            "docs/architecture/budget.md",

	"internal/adapters/responses": "docs/architecture/adapter-responses.md",

	"internal/knowledge/waymarks": "docs/architecture/knowledge-waymarks.md",

	"internal/adapters/anthropic": "docs/architecture/adapter-anthropic.md",

	"internal/adapters/chatcompletions": "docs/architecture/adapter-chat-completions.md",
	"internal/turn":                     "docs/architecture/turns.md",

	"internal/knowledge/personmap": "docs/architecture/knowledge-profile-and-map.md",
	"internal/contextbuild":        "docs/architecture/context.md",
	"internal/routing":             "docs/architecture/routing.md",
	"internal/usage":               "docs/architecture/usage.md",

	"internal/settingsapi":      "docs/architecture/settings-screens.md",
	"internal/tools":            "docs/architecture/tool-loop.md",
	"internal/tools/approval":   "docs/architecture/tool-loop.md",
	"internal/tools/builtin":    "docs/architecture/tool-loop.md",
	"internal/knowledge/recall": "docs/architecture/knowledge-recall.md",
	"internal/compact":          "docs/architecture/compaction.md",

	"internal/tools/web": "docs/architecture/web-search.md",

	"internal/backup":           "docs/architecture/backup.md",
	"internal/knowledge/memory": "docs/architecture/memory-pass.md",
	"internal/inspector":        "docs/architecture/inspector.md",

	"internal/recap":       "docs/architecture/chat-summaries.md",
	"internal/mcp":         "docs/architecture/connectors.md",
	"internal/mcp/mcptest": "docs/architecture/connectors.md",
	"internal/connectors":  "docs/architecture/connectors.md",

	"internal/attach":                        "docs/architecture/attach-and-handoff.md",
	"internal/turn/memoryscreens.go":         "docs/architecture/memory-screens.md",
	"internal/turn/incognito.go":             "docs/architecture/memory-screens.md",
	"internal/knowledge/memory/forget.go":    "docs/architecture/memory-screens.md",
	"internal/knowledge/memory/incognito.go": "docs/architecture/memory-screens.md",

	"internal/projects":           "docs/architecture/projects.md",
	"internal/embedding":          "docs/architecture/embeddings.md",
	"internal/turn/embeddings.go": "docs/architecture/embeddings.md",
}

var checks = []check{
	{"build", goCmd("build", "./...")},
	{"vet", goCmd("vet", "./...")},
	{"tests", goCmd("test", "./...")},
	{"settings registry", checkSettings},
	{"plan items", checkItems},
	{"state", checkState},
	{"docs", checkDocs},
	{"no secrets", checkSecrets},
	{"offline", checkOffline},
	{"tenant isolation", checkIsolation},
	{"fixed rules", checkFixedRules},
	{"no keys in output", checkNoKeys},
	{"test tenant", checkTestTenant},
	{"calls recorded", checkRecorded},
	{"context fit", checkContextFit},
	{"spend cap", checkSpendCap},
	{"approval", checkApproval},
	{"recall", checkRecall},
	{"sensitive content", checkSensitive},
	{"memory pass", checkMemory},
	{"background tasks", checkBackgroundTasks},
	{"past chats", checkPastChats},
	{"scheduled tasks", checkSchedules},
	{"unattended spend", checkUnattended},
	{"uploads", checkUploads},
	{"artifacts", checkArtifacts},
	{"attached chats", checkAttached},
	{"memory screens", checkMemoryScreens},
	{"projects", checkProjects},
	{"usage report", checkUsageReport},
	{"embeddings", checkEmbeddings},
	{"styles", checkStyles},
	{"wiki jobs", checkWikiJobs},
	{"operations", checkOperations},
	{"isolation drill", checkIsolationDrill},
	{"share", checkShare},
	{"sub-agents", checkSubagents},
	{"dictation", checkDictation},
	{"import", checkImport},
}

func main() {
	root, err := project.Root()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	failed := 0
	start := time.Now()
	for _, c := range checks {
		if err := c.run(root); err != nil {
			failed++
			fmt.Printf("FAIL  %s\n      %s\n", c.name, strings.ReplaceAll(err.Error(), "\n", "\n      "))
			continue
		}
		fmt.Printf("ok    %s\n", c.name)
	}
	fmt.Printf("\n%d of %d checks passed in %s\n", len(checks)-failed, len(checks), time.Since(start).Round(time.Millisecond))
	if failed > 0 {
		os.Exit(1)
	}
}

func goCmd(args ...string) func(string) error {
	return func(root string) error {
		out, err := project.Run(root, "go", args...)
		if err != nil {
			return fmt.Errorf("go %s failed:\n%s", strings.Join(args, " "), out)
		}
		return nil
	}
}

func checkSettings(string) error {
	reg, err := settings.Registry()
	if err != nil {
		return err
	}
	if len(reg) == 0 {
		return fmt.Errorf("the registry is empty")
	}
	// P11-04: every setting has a title and a short summary for the screen.
	if p := settings.CheckWords(reg); len(p) > 0 {
		return fmt.Errorf("%s", strings.Join(p, "\n"))
	}
	return nil
}

func checkItems(root string) error {
	items, err := project.Items(root)
	if err != nil {
		return err
	}
	if len(items) == 0 {
		return fmt.Errorf("docs/plan/items holds no work items")
	}
	if p := project.CheckItems(items); len(p) > 0 {
		return fmt.Errorf("%s", strings.Join(p, "\n"))
	}
	return nil
}

func checkState(root string) error {
	raw, err := os.ReadFile(filepath.Join(root, "docs", "plan", "STATE.md"))
	if err != nil {
		return err
	}
	var missing []string
	for _, key := range []string{"Phase:", "Next:", "Blocked:"} {
		if strings.Contains(project.StateLine(string(raw), key), "(missing") {
			missing = append(missing, key)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("STATE.md has no line starting %s", strings.Join(missing, ", "))
	}
	return nil
}

func checkDocs(root string) error {
	var problems []string
	for part, doc := range componentDocs {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(part))); err != nil {
			problems = append(problems, part+" is listed in verify and does not exist")
		}
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(doc))); err != nil {
			problems = append(problems, part+" has no doc at "+doc)
		}
	}
	// Every folder that holds Go code must be listed above.
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if project.Skip(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return nil
		}
		rel, _ := filepath.Rel(root, filepath.Dir(p))
		rel = filepath.ToSlash(rel)
		if _, ok := componentDocs[rel]; !ok {
			problems = append(problems, rel+" holds Go code and is not listed with a doc in tools/verify")
			componentDocs[rel] = ""
		}
		return nil
	})
	if err != nil {
		return err
	}
	for _, f := range []string{"docs/plan/PLAN.md", "docs/plan/LOOP.md", "docs/plan/PARALLEL.md", "docs/architecture/overview.md", "README.md"} {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(f))); err != nil {
			problems = append(problems, f+" is missing")
		}
	}
	// Every decision record must be linked from the overview.
	overview, _ := os.ReadFile(filepath.Join(root, "docs", "architecture", "overview.md"))
	records, err := filepath.Glob(filepath.Join(root, "docs", "architecture", "decisions", "*.md"))
	if err != nil {
		return err
	}
	if len(records) == 0 {
		problems = append(problems, "docs/architecture/decisions holds no decision records")
	}
	for _, r := range records {
		if link := "(decisions/" + filepath.Base(r) + ")"; !strings.Contains(string(overview), link) {
			problems = append(problems, "decision "+filepath.Base(r)+" is not linked from docs/architecture/overview.md")
		}
	}
	if len(problems) > 0 {
		return fmt.Errorf("%s", strings.Join(problems, "\n"))
	}
	return nil
}

// secretShapes are the shapes of real keys. A match anywhere in the repo fails verify.
var secretShapes = []*regexp.Regexp{
	regexp.MustCompile(`sk-ant-[A-Za-z0-9_-]{20,}`),
	regexp.MustCompile(`sk-(proj-|or-v1-)?[A-Za-z0-9]{32,}`),
	regexp.MustCompile(`AKIA[0-9A-Z]{16}`),
	regexp.MustCompile(`gh[pousr]_[A-Za-z0-9]{30,}`),
	regexp.MustCompile(`tvly-[A-Za-z0-9]{20,}`),
	regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----`),
}

func checkSecrets(root string) error {
	var found []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if project.Skip(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		info, err := d.Info()
		if err != nil || info.Size() > 2<<20 {
			return err
		}
		raw, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		for _, re := range secretShapes {
			if re.Match(raw) {
				rel, _ := filepath.Rel(root, p)
				found = append(found, filepath.ToSlash(rel)+" holds something shaped like a key")
				break
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	if len(found) > 0 {
		return fmt.Errorf("%s", strings.Join(found, "\n"))
	}
	return nil
}
