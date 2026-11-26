// Package projects keeps a person's projects (P9-01): each a name, the
// instructions every chat in it starts with, the knowledge files every
// call of those chats carries, the chats themselves, and the knowledge
// store space their facts are kept in.
//
// The package keeps the rows in the tenant's own database and the files'
// bytes in the tenant's files folder, reads the text out of text files, says
// what a call carries of a project, and gives fixed rules 1 and 3 their
// answers for projects:
//
//   - Rule 1. Every function takes one tenant's store and has no way to name
//     another. A chat joins a project through a row whose two columns refer
//     to that store's own chats and projects, so a chat or a project of
//     another tenant cannot be named.
//   - Rule 3. A file the person marked sensitive reaches a call only when
//     every model the call may reach may read sensitive content (MayShow).
//
// The bytes and the text go through two small seams, Blobs and TextReader,
// so the store the uploads lane (P9-02) builds for chat uploads and this one
// can become one (decision 0401).
//
// It reads no setting and does not import the chat record, which reads
// settings: the caller reads both and passes plain values in, as
// internal/attach does. docs/architecture/projects.md describes it.
package projects

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// Project is one project.
type Project struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Instructions go at the start of every call of every chat in the
	// project. They may be empty.
	Instructions string `json:"instructions"`
	// MemorySpace is the knowledge store space the project's chats keep
	// their facts in, and that recall in them searches first. Empty keeps
	// them with the person's other facts.
	MemorySpace string `json:"memory_space"`
	// Archived says the project is set aside. It is kept, and its chats are
	// still in it; the screens show it apart.
	Archived bool      `json:"archived"`
	Created  time.Time `json:"created"`
	Changed  time.Time `json:"changed"`
}

// File is one knowledge file of a project.
type File struct {
	// ID is the file's SHA-256 in lower-case hex: its name in the tenant's
	// files folder.
	ID        string `json:"id"`
	ProjectID string `json:"project_id"`
	// Name is the name the person's browser gave it, on one line. It is
	// shown and told to a model, and never used as a path.
	Name      string `json:"name"`
	MediaType string `json:"media_type"`
	Size      int64  `json:"size"`
	// Chars is how many characters of text were read from it.
	Chars int `json:"chars"`
	// Sensitive says the person marked the file sensitive: it reaches only
	// calls whose every model may read sensitive content.
	Sensitive bool      `json:"sensitive"`
	Added     time.Time `json:"added"`

	text string
}

// Text is the text read from the file when it was added.
func (f File) Text() string { return f.text }

// Options are the values of the settings this package is given. The caller
// reads them and passes them in.
type Options struct {
	// MaxProjects is the most projects a person may have, archived ones
	// included: projects.max_projects.
	MaxProjects int
	// NameMaxChars is the longest name: projects.name_max_chars.
	NameMaxChars int
	// InstructionsMaxChars is the longest instructions:
	// projects.instructions_max_chars.
	InstructionsMaxChars int
	// MaxFiles is the most files one project may have: projects.max_files.
	MaxFiles int
	// FileMaxBytes is the largest file: projects.file_max_bytes.
	FileMaxBytes int64
	// TextExtensions are the endings of the files taken, each with its dot,
	// in lower case: projects.text_extensions.
	TextExtensions []string
	// MemorySpace is the pattern a new project's memory space is named by:
	// projects.memory_space. {slug} becomes the project's name as a slug.
	MemorySpace string

	// Blobs keeps the files' bytes. Nil is Folder, the tenant's files
	// folder. It is not a setting: the integrator may give the store the
	// uploads share.
	Blobs Blobs
	// Read reads a file's text. Nil is ReadText. It is not a setting either.
	Read TextReader
}

// The setting each option is read from, and the settings the caller reads
// for Words and for recall. The package reads none of them.
const (
	SettingMaxProjects          = "projects.max_projects"
	SettingNameMaxChars         = "projects.name_max_chars"
	SettingInstructionsMaxChars = "projects.instructions_max_chars"
	SettingMaxFiles             = "projects.max_files"
	SettingFileMaxBytes         = "projects.file_max_bytes"
	SettingTextExtensions       = "projects.text_extensions"
	SettingMemorySpace          = "projects.memory_space"

	SettingFileContextMaxChars = "projects.file_context_max_chars"
	SettingInstructionsHeading = "projects.instructions_heading"
	SettingFileHeading         = "projects.file_heading"
	SettingFileCutNote         = "projects.file_cut_note"
	SettingRecallPrefer        = "projects.recall_prefer"
)

var (
	// ErrOptions says a setting holds a value nothing can be done with. The
	// rest of the error names it.
	ErrOptions = errors.New("projects: a setting is out of range")
	// ErrNotFound says the project, the file or the chat is not in this
	// tenant's record.
	ErrNotFound = errors.New("projects: not found")
	// ErrInvalid says what was given is refused: a name, instructions or a
	// file. The rest of the error says why, for a person.
	ErrInvalid = errors.New("projects: refused")
	// ErrFull says the person has as many projects, or the project as many
	// files, as they may.
	ErrFull = errors.New("projects: at the limit")
)

// CheckOptions refuses settings nothing can be done with, naming the
// setting.
func CheckOptions(o Options) error {
	bad := func(key, want string) error { return fmt.Errorf("%w: %s must be %s", ErrOptions, key, want) }
	switch {
	case o.MaxProjects < 1 || o.MaxProjects > 10000:
		return bad(SettingMaxProjects, "from 1 to 10000")
	case o.NameMaxChars < 1 || o.NameMaxChars > 500:
		return bad(SettingNameMaxChars, "from 1 to 500")
	case o.InstructionsMaxChars < 0 || o.InstructionsMaxChars > 200000:
		return bad(SettingInstructionsMaxChars, "from 0 to 200000")
	case o.MaxFiles < 0 || o.MaxFiles > 500:
		return bad(SettingMaxFiles, "from 0 to 500")
	case o.FileMaxBytes < 1 || o.FileMaxBytes > 1<<30:
		return bad(SettingFileMaxBytes, "from 1 to 1073741824")
	}
	for _, ext := range o.TextExtensions {
		if !strings.HasPrefix(ext, ".") || len(ext) < 2 || strings.ContainsAny(ext, " /\\") {
			return bad(SettingTextExtensions, "endings such as .txt, separated by commas")
		}
	}
	return nil
}

// Extensions reads projects.text_extensions: endings separated by commas,
// each given its dot and put in lower case. Blanks are left out.
func Extensions(list string) []string {
	var out []string
	for _, e := range strings.Split(list, ",") {
		e = strings.ToLower(strings.TrimSpace(e))
		if e == "" {
			continue
		}
		if !strings.HasPrefix(e, ".") {
			e = "." + e
		}
		out = append(out, e)
	}
	return out
}

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, args...))
}

func full(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrFull, fmt.Sprintf(format, args...))
}

// Reason is an error's words for a person, without this package's prefix.
func Reason(err error) string {
	text := err.Error()
	for _, e := range []error{ErrInvalid, ErrFull, ErrNotFound, ErrOptions} {
		if p := e.Error() + ": "; strings.HasPrefix(text, p) {
			return text[len(p):]
		}
	}
	return text
}
