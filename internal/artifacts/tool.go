package artifacts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jonathanmkosar/keystone/internal/audit"
	"github.com/jonathanmkosar/keystone/internal/tools"
)

// The tools' names.
const (
	CreateName = "create_artifact"
	UpdateName = "update_artifact"
	ReadName   = "read_artifact"
)

// Names are the three tools' names.
var Names = []string{CreateName, UpdateName, ReadName}

// Add registers the artifact tools. Each acts on the store and the chat of
// the turn that calls it, and on nothing else: a model makes, changes and
// reads only the artifacts of the chat it is in.
//
// The tools change nothing outside Keystone: an artifact is the model's
// work kept for the person, as a reply is, in their own folder, and every
// version is kept, so nothing is lost. So they say ReadOnly, which the
// approval rule reads as "runs without asking unless the person sets it
// to ask or never", and Inside, since what they return is Keystone's own.
// They never ask because a chat read outside content: that rule guards
// side effects outside Keystone (fixed rule 2), and an artifact is none.
// Decision 0420.
func Add(r *tools.Registry, o Options) error {
	if err := o.Check(); err != nil {
		return err
	}
	for _, t := range Tools(o) {
		if err := r.Add(t); err != nil {
			return err
		}
	}
	return nil
}

// Tools returns the three tools.
func Tools(o Options) []tools.Tool {
	return []tools.Tool{createTool(o), updateTool(o), readTool(o)}
}

const createDescription = "Make an artifact: a document the person sees in a panel beside the chat, kept with every version, which they can download and open on a page of its own. " +
	"Use it for substantial, self-contained work the person will want to keep, run, reuse or change: a program, a web page, a React component, an SVG picture, a Mermaid diagram, or a Markdown document longer than a few paragraphs. Short answers and snippets belong in the reply. " +
	"Types: code (give its language); html (one whole page, with its scripts and styles inline); react (one component as the default export; it may import react and nothing else, so style it with inline styles or a style element); svg; mermaid (the diagram's source alone, with no fence); markdown. " +
	"Artifacts are shown offline in a sandbox: nothing in one may load from the internet. After making one, say in a sentence what it is; do not repeat its content in the reply."

const updateDescription = "Change an artifact of this chat by writing a new version; every earlier version is kept and the person can go back to one. " +
	"Give artifact_id, and either content, the whole new document, or old_text and new_text, to replace one exact piece of text that appears once in the newest version. title, when given, renames it."

const readDescription = "Read the artifacts of this chat. With no artifact_id, lists them: each one's id, title, type and newest version. " +
	"With artifact_id, gives the newest version's content, or the version you name. Use it before changing an artifact whose content you no longer have."

func kindList() string {
	names := make([]string, len(Kinds))
	for i, k := range Kinds {
		names[i] = fmt.Sprintf("%q", k)
	}
	return strings.Join(names, ",")
}

func createTool(o Options) tools.Tool {
	return tools.Tool{
		Name:        CreateName,
		Description: createDescription,
		InputSchema: json.RawMessage(`{"type":"object","properties":{` +
			`"type":{"type":"string","enum":[` + kindList() + `],"description":"What the artifact is, which decides how it is shown."},` +
			`"title":{"type":"string","description":"A short name for it, in a few words."},` +
			`"language":{"type":"string","description":"For a code artifact, its language, such as python, go or typescript."},` +
			`"content":{"type":"string","description":"The whole document."}},` +
			`"required":["type","title","content"]}`),
		ReadOnly: true, // nothing outside Keystone changes (decision 0420)
		Inside:   true, // Keystone's own words
		Describe: func(input json.RawMessage) string {
			var args struct{ Type, Title string }
			json.Unmarshal(input, &args)
			if strings.TrimSpace(args.Title) == "" {
				return "Make an artifact."
			}
			return fmt.Sprintf("Make the %s artifact “%s”.", args.Type, strings.Join(strings.Fields(args.Title), " "))
		},
		Run: func(ctx context.Context, in tools.Input) (string, error) {
			var args struct {
				Type     string `json:"type"`
				Title    string `json:"title"`
				Language string `json:"language"`
				Content  string `json:"content"`
			}
			if err := json.Unmarshal(in.Args, &args); err != nil {
				return "", errors.New("the input does not read: give type, title and content, and language for code")
			}
			a, v, err := Create(ctx, in.Store, o, in.ChatID, Draft{Type: Kind(args.Type), Language: args.Language, Title: args.Title, Content: args.Content}, audit.RequestID(ctx))
			if err != nil {
				return "", forModel("the artifact was not made", err)
			}
			return said("Made", a, v), nil
		},
	}
}

func updateTool(o Options) tools.Tool {
	return tools.Tool{
		Name:        UpdateName,
		Description: updateDescription,
		InputSchema: json.RawMessage(`{"type":"object","properties":{` +
			`"artifact_id":{"type":"string","description":"The artifact's id, as create_artifact or read_artifact gave it."},` +
			`"content":{"type":"string","description":"The whole new document. Leave it out to replace one piece with old_text and new_text."},` +
			`"old_text":{"type":"string","description":"The exact text to replace, which appears once in the newest version."},` +
			`"new_text":{"type":"string","description":"What replaces old_text. Empty removes it."},` +
			`"title":{"type":"string","description":"A new title, when it should change."}},` +
			`"required":["artifact_id"]}`),
		ReadOnly: true, // nothing outside Keystone changes, and every version is kept (decision 0420)
		Inside:   true,
		Describe: func(input json.RawMessage) string {
			var args struct {
				ID string `json:"artifact_id"`
			}
			json.Unmarshal(input, &args)
			return fmt.Sprintf("Write a new version of the artifact %s.", strings.TrimSpace(args.ID))
		},
		Run: func(ctx context.Context, in tools.Input) (string, error) {
			var args struct {
				ID      string  `json:"artifact_id"`
				Content *string `json:"content"`
				OldText *string `json:"old_text"`
				NewText string  `json:"new_text"`
				Title   string  `json:"title"`
			}
			if err := json.Unmarshal(in.Args, &args); err != nil {
				return "", errors.New("the input does not read: give artifact_id, and content or old_text and new_text")
			}
			c := Change{Content: args.Content, NewText: args.NewText, Title: args.Title}
			if args.OldText != nil {
				c.OldText, c.HasOldText = *args.OldText, true
			}
			a, v, err := Update(ctx, in.Store, o, in.ChatID, strings.TrimSpace(args.ID), c, audit.RequestID(ctx))
			if err != nil {
				return "", forModel("the artifact was not changed", err)
			}
			return said("Wrote", a, v), nil
		},
	}
}

func readTool(o Options) tools.Tool {
	return tools.Tool{
		Name:        ReadName,
		Description: readDescription,
		InputSchema: json.RawMessage(`{"type":"object","properties":{` +
			`"artifact_id":{"type":"string","description":"The artifact to read. Leave it out to list this chat's artifacts."},` +
			`"version":{"type":"integer","description":"The version to read. Leave it out for the newest."}}}`),
		ReadOnly: true,
		Inside:   true, // the chat's own artifacts
		Run: func(ctx context.Context, in tools.Input) (string, error) {
			var args struct {
				ID      string `json:"artifact_id"`
				Version int    `json:"version"`
			}
			if err := json.Unmarshal(in.Args, &args); err != nil {
				return "", errors.New("the input does not read: give artifact_id, or nothing to list")
			}
			id := strings.TrimSpace(args.ID)
			if id == "" {
				list, err := List(ctx, in.Store, in.ChatID)
				if err != nil {
					return "", forModel("the artifacts could not be listed", err)
				}
				if len(list) == 0 {
					return "This chat has no artifacts.", nil
				}
				var b strings.Builder
				fmt.Fprintf(&b, "This chat's artifacts, the oldest first:\n")
				for _, a := range list {
					fmt.Fprintf(&b, "- “%s”: artifact %s, type %s%s, version %d\n", a.Title, a.ID, a.Type, languageOf(a), a.Latest)
				}
				return strings.TrimRight(b.String(), "\n"), nil
			}
			a, err := Get(ctx, in.Store, id)
			if err == nil && a.ChatID != in.ChatID {
				err = fmt.Errorf("artifact %s is not of this chat: %w", id, ErrNotFound)
			}
			if err != nil {
				return "", forModel("the artifact could not be read", err)
			}
			version := args.Version
			if version == 0 {
				version = a.Latest
			}
			v, content, err := Content(ctx, in.Store, id, version)
			if err != nil {
				return "", forModel("the artifact could not be read", err)
			}
			return fmt.Sprintf("“%s”: artifact %s, type %s%s, version %d of %d.\n\n%s", v.Title, a.ID, a.Type, languageOf(a), v.Version, a.Latest, content), nil
		},
	}
}

func languageOf(a Artifact) string {
	if a.Language == "" {
		return ""
	}
	return " (" + a.Language + ")"
}

// said is what the model reads after a version is written. The words
// "(artifact <id>, version <n>" are read by the chat screen too, to draw
// the artifact's card in the reply.
func said(verb string, a Artifact, v Version) string {
	s := fmt.Sprintf("%s the artifact “%s” (artifact %s, version %d, type %s%s). The person sees it in a panel beside the chat. Change it with update_artifact and artifact_id %s.",
		verb, v.Title, a.ID, v.Version, a.Type, languageOf(a), a.ID)
	if a.Sensitive {
		s += " It is marked sensitive, because this chat holds content from a sensitive space."
	}
	return s
}

// forModel is an error as the model reads it: what did not happen, and why
// in words it can act on.
func forModel(what string, err error) error {
	switch {
	case errors.Is(err, ErrRefused):
		return fmt.Errorf("%s: %s", what, strings.TrimPrefix(err.Error(), ErrRefused.Error()+": "))
	case errors.Is(err, ErrNotFound):
		return fmt.Errorf("%s: there is no such artifact in this chat; list them with read_artifact", what)
	}
	// Anything else is Keystone's own trouble, whose text may name the data
	// folder: the model is told only that it happened.
	return fmt.Errorf("%s: Keystone could not keep or read it", what)
}
