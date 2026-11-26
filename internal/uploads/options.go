package uploads

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Options are the install's limits on what may be uploaded, from
// settings/uploads.json. The caller reads them and passes them in: this
// package reads no setting.
type Options struct {
	// MaxBytes is the largest file one upload may be (uploads.max_bytes).
	MaxBytes int64 `json:"max_bytes"`
	// ImageTypes are the media types an image may have, known by its bytes
	// (uploads.image_types).
	ImageTypes []string `json:"image_types"`
	// TextExtensions are the endings of the text files whose text is read,
	// each with its dot, in lower case (uploads.text_extensions).
	TextExtensions []string `json:"text_extensions"`
	// DocumentTypes are the media types of the documents that are stored
	// and sent whole to a model that takes files, known by their bytes
	// (uploads.document_types).
	DocumentTypes []string `json:"document_types"`
}

// The install settings ReadOptions reads.
const (
	SettingMaxBytes       = "uploads.max_bytes"
	SettingImageTypes     = "uploads.image_types"
	SettingTextExtensions = "uploads.text_extensions"
	SettingDocumentTypes  = "uploads.document_types"
)

// The settings a person may set for themselves, which the turn reads for
// each call: how much of a file's text goes in, and the words a model is
// given in place of what it is not shown.
const (
	SettingTextMaxChars     = "uploads.text_max_chars"
	SettingFileHeading      = "uploads.file_heading"
	SettingFileMarker       = "uploads.file_marker"
	SettingTextCutNote      = "uploads.text_cut_note"
	SettingImageWithheld    = "uploads.image_withheld_note"
	SettingDocumentWithheld = "uploads.document_withheld_note"
)

// ReadOptions reads the options from wherever setting values are kept.
func ReadOptions(get func(key string) (string, bool)) (Options, error) {
	v := map[string]string{}
	for _, key := range []string{SettingMaxBytes, SettingImageTypes, SettingTextExtensions, SettingDocumentTypes} {
		s, ok := get(key)
		if !ok {
			return Options{}, fmt.Errorf("the %s setting is missing", key)
		}
		v[key] = s
	}
	var o Options
	var err error
	if o.MaxBytes, err = strconv.ParseInt(strings.TrimSpace(v[SettingMaxBytes]), 10, 64); err != nil {
		return Options{}, fmt.Errorf("%s: %w", SettingMaxBytes, err)
	}
	o.ImageTypes = Split(v[SettingImageTypes])
	o.DocumentTypes = Split(v[SettingDocumentTypes])
	for _, ext := range Split(v[SettingTextExtensions]) {
		if !strings.HasPrefix(ext, ".") {
			ext = "." + ext
		}
		o.TextExtensions = append(o.TextExtensions, ext)
	}
	return o, o.Check()
}

// Check refuses options no upload could be made under.
func (o Options) Check() error {
	switch {
	case o.MaxBytes < 1:
		return errors.New(SettingMaxBytes + " must be 1 or more")
	case len(o.ImageTypes)+len(o.TextExtensions)+len(o.DocumentTypes) == 0:
		return errors.New("the uploads settings take no kind of file at all")
	}
	for _, t := range append(append([]string(nil), o.ImageTypes...), o.DocumentTypes...) {
		if !strings.Contains(t, "/") {
			return fmt.Errorf("%q is not a media type such as image/png", t)
		}
	}
	for _, t := range o.ImageTypes {
		if !strings.HasPrefix(t, "image/") {
			return fmt.Errorf("%s: %q is not an image type", SettingImageTypes, t)
		}
	}
	return nil
}

// Split splits a list as a setting holds it: separated by commas, with
// spaces around an entry ignored, an empty entry dropped, and every entry
// in lower case.
func Split(list string) []string {
	var out []string
	for _, part := range strings.Split(list, ",") {
		if part = strings.ToLower(strings.TrimSpace(part)); part != "" {
			out = append(out, part)
		}
	}
	return out
}
