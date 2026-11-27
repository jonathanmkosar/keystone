package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/jonathanmkosar/keystone/internal/project"
	"github.com/jonathanmkosar/keystone/settings"
)

// Settings at a glance (P11-04): the General screen shows each setting by
// its name and one short line, and gives the whole explanation once its
// row is opened. docs/architecture/settings-screens.md describes it.

// checkSettingsGlance holds the screen to what the item promised: every
// setting of the registry has a title and a short summary; what a row and
// its opened part show is worked out in settingsView.ts, a file with no
// imports whose tests run against the real settings/*.json when Node is on
// this computer (decision 0501); the screen draws the words from that file
// alone; and on a phone no box takes the focus by itself, every box is
// 16px, and an overlay, if one is ever added, follows the visible part of
// the page.
func checkSettingsGlance(root string) error {
	var problems []string
	reg, err := settings.Registry()
	if err != nil {
		return err
	}
	problems = append(problems, settings.CheckWords(reg)...)
	problems = append(problems, settingsGlanceSourceProblems(root)...)
	problems = append(problems, settingsGlanceTestProblems(root)...)
	if len(problems) > 0 {
		return fmt.Errorf("%s", strings.Join(problems, "\n"))
	}
	return nil
}

func settingsGlanceSourceProblems(root string) []string {
	var problems []string
	src := filepath.Join(root, "web", "app", "src")
	read := func(name string) string {
		raw, err := os.ReadFile(filepath.Join(src, name))
		if err != nil {
			problems = append(problems, "web/app/src/"+name+", part of the General settings screen, cannot be read: "+err.Error())
		}
		return string(raw)
	}
	view := read("settingsView.ts")
	screen := read("GeneralSettings.tsx")
	sheet := read("settings-general.css")
	if len(problems) > 0 {
		return problems
	}

	// The view has no imports, so Node runs it as it is.
	if regexp.MustCompile(`(?m)^\s*import\s`).MatchString(view) {
		problems = append(problems, "web/app/src/settingsView.ts imports something; Node's test runner runs it as it is, so it is given all it uses")
	}
	// The screen draws a row from rowOf and its opened part from moreOf, and
	// takes the words from nowhere else: the short line cannot be dropped,
	// and the long text cannot come back into the row, without the tests of
	// settingsView.ts seeing it.
	for _, want := range []string{"rowOf(", "moreOf(", "groupsOf(", "keeps("} {
		if !strings.Contains(screen, want) {
			problems = append(problems, "web/app/src/GeneralSettings.tsx no longer calls "+want+"...) of settingsView.ts, which is what the screen's tests prove")
		}
	}
	if m := regexp.MustCompile(`\b(setting|s)\.(description|summary|title)\b`).FindString(screen); m != "" {
		problems = append(problems, "web/app/src/GeneralSettings.tsx reads "+m+" itself; the words a row shows come from rowOf and moreOf in settingsView.ts")
	}
	if !strings.Contains(screen, "aria-expanded={open}") || !strings.Contains(screen, "{open && (") {
		problems = append(problems, "web/app/src/GeneralSettings.tsx no longer opens a row in place with a button that says whether it is open (aria-expanded)")
	}
	// On a phone: nothing takes the focus by itself, so no keyboard rises
	// over the list.
	for _, bad := range []string{"autoFocus", ".focus("} {
		if strings.Contains(screen, bad) {
			problems = append(problems, "web/app/src/GeneralSettings.tsx names "+bad+": no box of the General screen takes the focus by itself")
		}
	}
	// Every box of the settings screens is 16px at phone width, or a
	// phone's browser zooms the page in when one takes the focus.
	phone := sheet
	if i := strings.Index(sheet, "@media (max-width: 760px)"); i >= 0 {
		phone = sheet[i:]
	} else {
		phone = ""
	}
	if !regexp.MustCompile(`\.settings \.input\s*\{\s*font-size:\s*16px;`).MatchString(phone) {
		problems = append(problems, "web/app/src/settings-general.css no longer sets .settings .input to 16px at phone width; a smaller box makes a phone's browser zoom the page")
	}
	// The screen has no overlay: the long text opens in place. One added
	// later must follow the visible part of the page, as the attach picker
	// does, or the keyboard squashes it.
	if strings.Contains(sheet, "position: fixed") && !strings.Contains(screen, "visualViewport") {
		problems = append(problems, "web/app/src/settings-general.css has a fixed overlay and GeneralSettings.tsx does not follow window.visualViewport; on a phone the keyboard would cover it")
	}
	return problems
}

// settingsGlanceTestProblems runs web/app/test/settingsView.test.ts with
// Node's own test runner. Without Node it runs nothing, as the dictation
// check does: Keystone builds with Go alone (decision 0005), and the
// registry and source checks above still hold.
func settingsGlanceTestProblems(root string) []string {
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
		return []string{"node " + ver + " cannot run the settings screen's tests, which need Node 22.6 or later; update Node"}
	}
	out, err := project.Run(app, node, "--experimental-strip-types", "--test", filepath.Join("test", "settingsView.test.ts"))
	if err != nil {
		return []string{"the settings screen's tests failed (npm test in web/app):\n" + out}
	}
	if !strings.Contains(out, "fail 0") {
		return []string{"the settings screen's tests did not report their result:\n" + out}
	}
	return nil
}
