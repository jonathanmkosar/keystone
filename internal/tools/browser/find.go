package browser

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

// FindProgram returns the browser program the agent's browser runs: the
// one named, which must exist, or else the first Chromium-family browser
// found in its usual place, Microsoft Edge first. ErrNoBrowser says there
// is none.
func FindProgram(named string) (string, error) {
	if named != "" {
		info, err := os.Stat(named)
		if err != nil || info.IsDir() {
			return "", fmt.Errorf("agents.browser_path names %s, and there is no program there", named)
		}
		return named, nil
	}
	for _, p := range candidates() {
		if p == "" {
			continue
		}
		if !filepath.IsAbs(p) {
			if found, err := exec.LookPath(p); err == nil {
				return found, nil
			}
			continue
		}
		if info, err := os.Stat(p); err == nil && !info.IsDir() {
			return p, nil
		}
	}
	return "", ErrNoBrowser
}

// candidates are the usual places of a Chromium-family browser on this
// operating system, Edge first, then Chrome, then Chromium.
func candidates() []string {
	switch runtime.GOOS {
	case "windows":
		var out []string
		for _, rel := range []string{`Microsoft\Edge\Application\msedge.exe`, `Google\Chrome\Application\chrome.exe`, `Chromium\Application\chrome.exe`} {
			for _, env := range []string{"ProgramFiles(x86)", "ProgramFiles", "LOCALAPPDATA"} {
				if base := os.Getenv(env); base != "" {
					out = append(out, filepath.Join(base, rel))
				}
			}
		}
		return out
	case "darwin":
		return []string{
			"/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge",
			"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
			"/Applications/Chromium.app/Contents/MacOS/Chromium",
		}
	}
	return []string{"microsoft-edge", "microsoft-edge-stable", "google-chrome", "google-chrome-stable", "chromium", "chromium-browser"}
}
