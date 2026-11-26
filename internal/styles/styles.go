// Package styles keeps a person's styles (P9-05): saved presets of tone and
// format, each a name and instructions, and the style each chat uses. A
// chat's every call carries its style's instructions in the standing
// start's style slot, so a chat that keeps its style keeps a start a
// provider can cache.
//
// A person has two kinds of style:
//
//   - The built-in ones, which every person has and none can change. They
//     are not rows: the caller reads them from the setting styles.builtin
//     (ParseBuiltIn) and passes them in.
//   - Their own, rows of the tenant's own database, which they make, change
//     and delete.
//
// A chat uses its own style when it has one, and the person's default
// otherwise (Resolve). A chat's first turn keeps the default it used as the
// chat's own (Keep), so changing the default later reaches new chats only.
//
// The package gives fixed rule 1 its answer for styles: every function
// takes one tenant's store and has no way to name another, and a chat is
// given a style through a row whose chat column refers to that store's own
// chats. A style is words in the system text and nothing more: the package
// knows nothing of routing, recall or approvals, and what a style says
// changes none of them.
//
// It reads no setting and does not import the chat record, which reads
// settings: the caller reads both and passes plain values in, as
// internal/projects does. docs/architecture/styles.md describes it.
package styles

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// Style is one style: a built-in one or one of the person's own.
type Style struct {
	// ID is a built-in style's id from styles.builtin, or sixteen
	// hexadecimal characters for one of the person's own.
	ID   string `json:"id"`
	Name string `json:"name"`
	// Instructions say how replies should be written. A chat that uses the
	// style is given them at the start of every call. A built-in style may
	// have none, and then sends nothing.
	Instructions string `json:"instructions"`
	// BuiltIn says the style comes from styles.builtin: every person has
	// it, and none can change or delete it.
	BuiltIn bool `json:"built_in"`
	// Created and Changed are set for the person's own styles only.
	Created time.Time `json:"created,omitzero"`
	Changed time.Time `json:"changed,omitzero"`
}

// Options are the values of the settings this package is given. The caller
// reads them and passes them in.
type Options struct {
	// BuiltIn are the built-in styles, in the order they are offered:
	// styles.builtin, read with ParseBuiltIn.
	BuiltIn []Style
	// MaxStyles is the most styles of their own a person may have:
	// styles.max_styles.
	MaxStyles int
	// NameMaxChars is the longest name: styles.name_max_chars.
	NameMaxChars int
	// InstructionsMaxChars is the longest instructions:
	// styles.instructions_max_chars.
	InstructionsMaxChars int
}

// The setting each option is read from, and the settings the caller reads
// for the default style and for the words around a style. The package
// reads none of them.
const (
	SettingBuiltIn              = "styles.builtin"
	SettingMaxStyles            = "styles.max_styles"
	SettingNameMaxChars         = "styles.name_max_chars"
	SettingInstructionsMaxChars = "styles.instructions_max_chars"
	SettingDefault              = "styles.default"
	SettingHeading              = "styles.heading"
)

// Where the style a chat uses comes from.
const (
	// SourceChat: the chat's own style, chosen for it or kept by its first
	// turn.
	SourceChat = "chat"
	// SourceDefault: the person's default style, styles.default, since the
	// chat has none of its own yet.
	SourceDefault = "default"
	// SourceNone: neither names a style that exists, so the chat uses none.
	SourceNone = "none"
)

var (
	// ErrOptions says a setting holds a value nothing can be done with. The
	// rest of the error names it.
	ErrOptions = errors.New("styles: a setting is out of range")
	// ErrNotFound says the style or the chat is not in this tenant's record.
	ErrNotFound = errors.New("styles: not found")
	// ErrInvalid says a name or instructions are refused. The rest of the
	// error says why, for a person.
	ErrInvalid = errors.New("styles: refused")
	// ErrFull says the person has as many styles of their own as they may.
	ErrFull = errors.New("styles: at the limit")
	// ErrBuiltIn says a built-in style was to be changed or deleted, which
	// no person may do.
	ErrBuiltIn = errors.New("styles: a built-in style")
)

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, args...))
}

func full(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrFull, fmt.Sprintf(format, args...))
}

// Reason is an error's words for a person, without this package's prefix.
func Reason(err error) string {
	text := err.Error()
	for _, e := range []error{ErrInvalid, ErrFull, ErrNotFound, ErrOptions, ErrBuiltIn} {
		if p := e.Error() + ": "; strings.HasPrefix(text, p) {
			return text[len(p):]
		}
	}
	return text
}

// CheckOptions refuses settings nothing can be done with, naming the
// setting.
func CheckOptions(o Options) error {
	bad := func(key, want string) error { return fmt.Errorf("%w: %s must be %s", ErrOptions, key, want) }
	switch {
	case o.MaxStyles < 0 || o.MaxStyles > 1000:
		return bad(SettingMaxStyles, "from 0 to 1000")
	case o.NameMaxChars < 1 || o.NameMaxChars > 200:
		return bad(SettingNameMaxChars, "from 1 to 200")
	case o.InstructionsMaxChars < 1 || o.InstructionsMaxChars > 50000:
		return bad(SettingInstructionsMaxChars, "from 1 to 50000")
	}
	return nil
}

// ownID is the shape of the id of a person's own style. No built-in
// style's id may have it, so the two kinds never meet.
var ownID = regexp.MustCompile(`^[0-9a-f]{16}$`)

// builtInID is the shape of a built-in style's id.
var builtInID = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,39}$`)

// IsOwnID says whether an id has the shape of a person's own style's id.
func IsOwnID(id string) bool { return ownID.MatchString(id) }

// ParseBuiltIn reads styles.builtin: a JSON list of objects, each with an
// id, a name and instructions. It refuses a list that does not read, an id
// of the wrong shape or one that is there twice, a blank name or one that
// is there twice, and a field it does not know, naming the setting.
func ParseBuiltIn(raw string) ([]Style, error) {
	bad := func(format string, args ...any) error {
		return fmt.Errorf("%w: %s: %s", ErrOptions, SettingBuiltIn, fmt.Sprintf(format, args...))
	}
	var list []struct {
		ID           string `json:"id"`
		Name         string `json:"name"`
		Instructions string `json:"instructions"`
	}
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	dec := json.NewDecoder(strings.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&list); err != nil {
		return nil, bad("it is a JSON list of objects with an id, a name and instructions: %v", err)
	}
	var out []Style
	for _, b := range list {
		switch {
		case !builtInID.MatchString(b.ID) || IsOwnID(b.ID):
			return nil, bad("the id %q is not lower-case letters, digits and hyphens, or it is sixteen hexadecimal characters", b.ID)
		case oneLine(b.Name) == "":
			return nil, bad("the style %q has no name", b.ID)
		}
		st := Style{ID: b.ID, Name: oneLine(b.Name), Instructions: tidy(b.Instructions), BuiltIn: true}
		for _, have := range out {
			if have.ID == st.ID {
				return nil, bad("the id %q is there twice", st.ID)
			}
			if strings.EqualFold(have.Name, st.Name) {
				return nil, bad("the name %q is there twice", st.Name)
			}
		}
		out = append(out, st)
	}
	return out, nil
}

// oneLine puts a name on one line with single spaces.
func oneLine(name string) string {
	return strings.Join(strings.FieldsFunc(name, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }), " ")
}

// tidy keeps instructions as they were written, with line ends made \n and
// the spaces at either end taken off.
func tidy(text string) string {
	return strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n"))
}

// cleanName refuses a name that is empty or too long.
func cleanName(name string, max int) (string, error) {
	name = oneLine(name)
	if n := utf8.RuneCountInString(name); n == 0 || n > max {
		return "", invalid("a style's name is 1 to %d characters", max)
	}
	return name, nil
}

// cleanInstructions refuses instructions that are empty or too long.
func cleanInstructions(text string, max int) (string, error) {
	text = tidy(text)
	n := utf8.RuneCountInString(text)
	switch {
	case n == 0:
		return "", invalid("a style needs instructions: how replies should be written")
	case n > max:
		return "", invalid("a style's instructions are at most %d characters, and these are %d", max, n)
	}
	return text, nil
}

// Text is what the style part of a call's standing start holds: the
// heading, with {name} the style's name, a blank line and the style's
// instructions; the instructions alone when the heading is empty; and
// nothing when the style has no instructions, as Normal has none. It
// changes only when the person changes the chat's style, or the style's
// name or instructions, so a provider caches it with the rest of the start.
func Text(heading string, st Style) string {
	text := strings.TrimSpace(st.Instructions)
	if text == "" {
		return ""
	}
	heading = strings.TrimSpace(strings.ReplaceAll(heading, "{name}", st.Name))
	if heading == "" {
		return text
	}
	return heading + "\n\n" + text
}
