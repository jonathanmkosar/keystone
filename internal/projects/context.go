package projects

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"
)

// MayShow is fixed rule 3's answer for one knowledge file in one call.
// mayReadSensitive is the answer for the least of the models the call's
// route may reach. A file marked sensitive is shown only when every one of
// them may read sensitive content. When it is shown, the chat it goes into
// must be held first (recall.Hold), since what a call is given stays in the
// chat and is sent again with every later call of it (decision 0403).
func MayShow(f File, mayReadSensitive bool) bool {
	return !f.Sensitive || mayReadSensitive
}

// LeftOutLabel is what the turn inspector calls the line LeftOut gives.
const LeftOutLabel = "project files left out"

// LeftOut is the line a call is given in place of the files it may not
// read. It names no file.
func LeftOut(n int) string {
	files := "files of this chat's project are"
	if n == 1 {
		files = "file of this chat's project is"
	}
	return fmt.Sprintf("%d knowledge %s left out of this call: the person marked them sensitive, and only a local or trusted model may read them. If the person asks about them, say so.", n, files)
}

// Words are the values of the settings that shape what a call carries of
// a project. The caller reads them and passes them in.
type Words struct {
	// InstructionsHeading goes before the instructions. {name} becomes the
	// project's name: projects.instructions_heading.
	InstructionsHeading string
	// FileHeading goes before a file's text. {project} becomes the
	// project's name and {name} the file's: projects.file_heading.
	FileHeading string
	// CutNote follows a file's text that was cut. {name} becomes the
	// file's name and {chars} how many characters were left out:
	// projects.file_cut_note.
	CutNote string
	// FileMaxChars is the most characters of one file's text a call is
	// given: projects.file_context_max_chars.
	FileMaxChars int
}

// CheckWords refuses words a call cannot be given.
func CheckWords(w Words) error {
	if w.FileMaxChars < 100 || w.FileMaxChars > 1000000 {
		return fmt.Errorf("%w: %s must be from 100 to 1000000", ErrOptions, SettingFileContextMaxChars)
	}
	return nil
}

// fill puts values in a pattern's {names}.
func fill(pattern string, values ...string) string {
	pairs := make([]string, 0, len(values))
	for i := 0; i+1 < len(values); i += 2 {
		pairs = append(pairs, "{"+values[i]+"}", values[i+1])
	}
	return strings.NewReplacer(pairs...).Replace(pattern)
}

// Instructions is what the project part of a call's standing start holds:
// the heading and the project's instructions, or nothing when it has none.
// It changes only when the person changes the project's name or its
// instructions, so a provider caches it with the rest of the start.
func Instructions(w Words, p Project) string {
	text := strings.TrimSpace(p.Instructions)
	if text == "" {
		return ""
	}
	heading := strings.TrimSpace(fill(w.InstructionsHeading, "name", p.Name))
	if heading == "" {
		return text
	}
	return heading + "\n\n" + text
}

// FileItem is one knowledge file as a call carries it, with the
// attachments: a label for the turn inspector, and the text that is sent,
// which is the heading and the file's text, cut to w.FileMaxChars with the
// cut note after it.
func FileItem(w Words, p Project, f File) (label, text string) {
	label = fmt.Sprintf("project %q: %s", p.Name, f.Name)
	body := strings.TrimSpace(f.Text())
	n := utf8.RuneCountInString(body)
	if n > w.FileMaxChars {
		body = strings.TrimRight(string([]rune(body)[:w.FileMaxChars]), " \n\t") + "\n" +
			fill(w.CutNote, "name", f.Name, "chars", strconv.Itoa(n-w.FileMaxChars))
	}
	heading := strings.TrimSpace(fill(w.FileHeading, "project", p.Name, "name", f.Name))
	if heading == "" {
		return label, body
	}
	return label, heading + "\n" + body
}
