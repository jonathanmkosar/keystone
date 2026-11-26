package artifacts

import (
	"strconv"
	"strings"
)

// extensions are the file endings of code artifacts, by language. A
// language not listed is saved as text.
var extensions = map[string]string{
	"bash": "sh", "c": "c", "c#": "cs", "c++": "cpp", "clojure": "clj", "cpp": "cpp", "cs": "cs", "csharp": "cs",
	"css": "css", "csv": "csv", "dart": "dart", "elixir": "ex", "erlang": "erl", "go": "go", "golang": "go",
	"haskell": "hs", "html": "html", "ini": "ini", "java": "java", "javascript": "js", "js": "js", "json": "json",
	"jsx": "jsx", "kotlin": "kt", "lua": "lua", "markdown": "md", "md": "md", "perl": "pl", "php": "php",
	"powershell": "ps1", "ps1": "ps1", "py": "py", "python": "py", "r": "r", "rb": "rb", "ruby": "rb", "rust": "rs",
	"rs": "rs", "scala": "scala", "sh": "sh", "shell": "sh", "sql": "sql", "swift": "swift", "text": "txt",
	"toml": "toml", "ts": "ts", "tsx": "tsx", "typescript": "ts", "xml": "xml", "yaml": "yaml", "yml": "yaml",
	"zsh": "sh",
}

// FileOf is the name and the media type one version is saved under: its
// title made safe as a file's name, the version when it is not the newest,
// and the ending its type has.
func FileOf(a Artifact, v Version) (name, media string) {
	ext, media := "txt", "text/plain; charset=utf-8"
	switch a.Type {
	case KindHTML:
		ext, media = "html", "text/html; charset=utf-8"
	case KindReact:
		ext = "jsx"
	case KindSVG:
		ext, media = "svg", "image/svg+xml"
	case KindMermaid:
		ext = "mmd"
	case KindMarkdown:
		ext, media = "md", "text/markdown; charset=utf-8"
	case KindCode:
		if e, ok := extensions[a.Language]; ok {
			ext = e
		}
	}
	base := safeName(v.Title)
	if v.Version != a.Latest {
		base += " (version " + strconv.Itoa(v.Version) + ")"
	}
	return base + "." + ext, media
}

// safeName is a title as a file's name: the characters no system takes in
// one become dashes, control characters go, and it is cut to a length
// every system takes.
func safeName(title string) string {
	var b strings.Builder
	for _, r := range title {
		switch {
		case r < 0x20 || r == 0x7f:
		case strings.ContainsRune(`<>:"/\|?*`, r):
			b.WriteRune('-')
		default:
			b.WriteRune(r)
		}
	}
	name := strings.Trim(strings.Join(strings.Fields(b.String()), " "), " .")
	if runes := []rune(name); len(runes) > 80 {
		name = strings.TrimRight(string(runes[:80]), " .")
	}
	if name == "" {
		name = "artifact"
	}
	return name
}
