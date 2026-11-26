package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/jonathanmkosar/keystone/internal/project"
	"github.com/jonathanmkosar/keystone/settings"
)

// dictationFiles are the dictation path: the machine and the screen's part.
var dictationFiles = []string{"dictation.ts", "DictationControls.tsx"}

// checkDictation runs the dictation machine's tests with Node's own test
// runner when Node is on this computer (decision 0501), and always reads
// the source: the dictation path names no send and no network call, the
// composer asks dictation before it sends, the server lets the microphone
// be used by Keystone's own pages only with its content security policy
// unchanged, and the voice settings are in the registry.
func checkDictation(root string) error {
	problems := dictationSourceProblems(root)
	if p := dictationSettingsProblems(); len(p) > 0 {
		problems = append(problems, p...)
	}
	if p := dictationTestProblems(root); len(p) > 0 {
		problems = append(problems, p...)
	}
	if len(problems) > 0 {
		return fmt.Errorf("%s", strings.Join(problems, "\n"))
	}
	return nil
}

// dictationForbidden are what the dictation path must never name: a way to
// send a message, submit the form, or reach the network.
var dictationForbidden = []string{"onSend", "sendMessage", "createChat", "startProjectChat", "createIncognitoChat", "requestSubmit", ".submit(", "fetch(", "sendBeacon", "XMLHttpRequest", "WebSocket", "EventSource"}

func dictationSourceProblems(root string) []string {
	var problems []string
	src := filepath.Join(root, "web", "app", "src")
	for _, name := range dictationFiles {
		raw, err := os.ReadFile(filepath.Join(src, name))
		if err != nil {
			problems = append(problems, "web/app/src/"+name+", part of dictation, cannot be read: "+err.Error())
			continue
		}
		for _, bad := range dictationForbidden {
			if bytes.Contains(raw, []byte(bad)) {
				problems = append(problems, "web/app/src/"+name+" names "+bad+": dictation must never send or reach the network")
			}
		}
	}
	// The machine has no imports, so Node runs it as it is, and so it can
	// reach nothing but what it is given.
	if raw, err := os.ReadFile(filepath.Join(src, "dictation.ts")); err == nil && regexp.MustCompile(`(?m)^\s*import\s`).Match(raw) {
		problems = append(problems, "web/app/src/dictation.ts imports something; the machine is given all it uses")
	}
	// The screen's part reads only the settings from the API.
	if raw, err := os.ReadFile(filepath.Join(src, "DictationControls.tsx")); err == nil {
		for _, line := range strings.Split(string(raw), "\n") {
			if strings.Contains(line, `from "./api"`) && strings.TrimSpace(line) != `import { listSettings } from "./api";` {
				problems = append(problems, "DictationControls.tsx takes more than listSettings from api.ts: "+strings.TrimSpace(line))
			}
		}
	}
	composer, err := os.ReadFile(filepath.Join(src, "Composer.tsx"))
	if err != nil {
		return append(problems, "web/app/src/Composer.tsx cannot be read: "+err.Error())
	}
	c := string(composer)
	start := strings.Index(c, "function send()")
	hold := strings.Index(c, "if (dictation.holdsSend()) return;")
	call := strings.Index(c, "onSend(text")
	switch {
	case start < 0 || call < 0:
		problems = append(problems, "Composer.tsx has no send() that calls onSend; the dictation check cannot find the send action")
	case hold < start || hold > call:
		problems = append(problems, "Composer.tsx's send() does not ask dictation.holdsSend() before onSend, so Enter or Send during dictation could send")
	}
	if strings.Count(c, "onSend(") != 1 {
		problems = append(problems, "Composer.tsx calls onSend in more than one place; only send(), which asks dictation first, may")
	}
	if !strings.Contains(c, "useDictation(area, text, setText)") {
		problems = append(problems, "Composer.tsx does not give dictation only its box and its text")
	}
	serve, err := os.ReadFile(filepath.Join(root, "cmd", "keystone", "serve.go"))
	if err != nil {
		return append(problems, "cmd/keystone/serve.go cannot be read: "+err.Error())
	}
	if !bytes.Contains(serve, []byte(`h.Set("Permissions-Policy", "microphone=(self)")`)) {
		problems = append(problems, "cmd/keystone/serve.go no longer allows the microphone to Keystone's own origin alone (Permissions-Policy: microphone=(self))")
	}
	if !bytes.Contains(serve, []byte(`h.Set("Content-Security-Policy", "default-src 'self'")`)) {
		problems = append(problems, "the main page's content security policy is no longer default-src 'self' in cmd/keystone/serve.go")
	}
	return problems
}

func dictationSettingsProblems() []string {
	reg, err := settings.Registry()
	if err != nil {
		return []string{err.Error()}
	}
	want := map[string]string{"voice.dictation": "bool", "voice.language": "string", "voice.interim": "bool", "voice.max_listen": "duration", "voice.finish_wait": "duration"}
	var problems []string
	for key, typ := range want {
		s, ok := settings.Lookup(reg, key)
		if !ok {
			problems = append(problems, key+" is not in the settings registry; the composer draws no microphone without it")
			continue
		}
		if s.Type != typ {
			problems = append(problems, fmt.Sprintf("%s is a %s, and the screen reads it as a %s", key, s.Type, typ))
		}
	}
	return problems
}

// dictationTestProblems runs web/app/test/dictation.test.ts with Node's
// own test runner. Without Node it runs nothing: Keystone builds with Go
// alone (decision 0005), and the source checks above still hold. A Node
// too old to read TypeScript (before 22.6) is a problem, not a skip.
func dictationTestProblems(root string) []string {
	node, err := exec.LookPath("node")
	if err != nil {
		return nil
	}
	app := filepath.Join(root, "web", "app")
	ver, err := project.Run(app, node, "--version")
	if err != nil {
		return []string{"node --version failed: " + ver}
	}
	if !nodeReadsTypeScript(ver) {
		return []string{"node " + ver + " cannot run the dictation tests, which need Node 22.6 or later; update Node"}
	}
	out, err := project.Run(app, node, "--experimental-strip-types", "--test", filepath.Join("test", "dictation.test.ts"))
	if err != nil {
		return []string{"the dictation tests failed (npm test in web/app):\n" + out}
	}
	if !strings.Contains(out, "fail 0") {
		return []string{"the dictation tests did not report their result:\n" + out}
	}
	return nil
}

// nodeReadsTypeScript reports whether a `node --version` answer is 22.6 or later.
func nodeReadsTypeScript(version string) bool {
	parts := strings.SplitN(strings.TrimPrefix(strings.TrimSpace(version), "v"), ".", 3)
	if len(parts) < 2 {
		return false
	}
	major, err1 := strconv.Atoi(parts[0])
	minor, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil {
		return false
	}
	return major > 22 || major == 22 && minor >= 6
}
