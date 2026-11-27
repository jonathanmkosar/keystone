package turn

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/jonathanmkosar/keystone/internal/chat"
	"github.com/jonathanmkosar/keystone/internal/tenant"
	"github.com/jonathanmkosar/keystone/settings"
)

// This file is location per message (P10-06). A person taps "Use my
// location" in the composer; the browser is asked for the position once;
// the page rounds it and sends it with that message, in the send body's
// location field (decision 0600). Here it is rounded again to the person's
// setting, kept with the message in message_locations, and given to the
// model as one plain line at the start of that message, before the
// person's words, in every call that sends the message (decision 0601).
// Nothing reads a position: it only ever arrives in a message, and only on
// the person's tap.

// The settings of location per message, in settings/location.json.
const (
	KeyLocationShare    = "location.share"
	KeyLocationDecimals = "location.decimals"
	KeyLocationTimeout  = "location.timeout" // read by the page only
	KeyLocationLine     = "location.line"
)

// The fewest and most decimals a position may keep. They bound the
// registry's options for location.decimals, and are not themselves a
// setting: fewer than one says nothing useful, and more than four is closer
// than a building.
const (
	minLocationDecimals = 1
	maxLocationDecimals = 4
	// defaultLocationDecimals is used when the setting cannot be read.
	defaultLocationDecimals = 2
)

// Location is a position as the page sends it with a message: latitude and
// longitude in decimal degrees, already rounded by the page. The server
// rounds again, to the person's setting, so nothing more exact is kept or
// sent whatever a client sends.
type Location struct {
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
}

// SentLocation is the location kept with a person's message: the rounded
// position, how many decimals it kept, and the line the model was given.
type SentLocation struct {
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
	// Decimals is how many decimal places the position kept: 2 is about a
	// kilometre.
	Decimals int `json:"decimals"`
	// Line is the sentence the model was given before the person's words.
	Line string `json:"line"`
}

// roundTo rounds a coordinate to a number of decimal places, with no
// negative zero.
func roundTo(v float64, decimals int) float64 {
	p := math.Pow10(decimals)
	r := math.Round(v*p) / p
	if r == 0 {
		return 0
	}
	return r
}

// clampDecimals keeps a number of decimals between the fewest and the most.
func clampDecimals(n int) int {
	return max(minLocationDecimals, min(maxLocationDecimals, n))
}

// distanceWords says, for a person and a model, about how far a position
// rounded to a number of decimals may be from where it was read: a degree
// of latitude is about 111 km.
func distanceWords(decimals int) string {
	switch clampDecimals(decimals) {
	case 1:
		return "11 km"
	case 2:
		return "1 km"
	case 3:
		return "110 m"
	default:
		return "11 m"
	}
}

// formatCoordinate writes a rounded coordinate with exactly its decimals,
// so 38.6 kept to two places reads 38.60.
func formatCoordinate(v float64, decimals int) string {
	return strconv.FormatFloat(v, 'f', decimals, 64)
}

// locationLine fills the line's template with a rounded position.
func locationLine(template string, lat, lon float64, decimals int) string {
	return strings.NewReplacer(
		"{latitude}", formatCoordinate(lat, decimals),
		"{longitude}", formatCoordinate(lon, decimals),
		"{distance}", distanceWords(decimals),
	).Replace(template)
}

// locationDecimals reads the person's setting for how many decimals a
// position keeps.
func (r *Runner) locationDecimals(ctx context.Context, s *tenant.Store) int {
	n, err := strconv.Atoi(r.setting(ctx, s, KeyLocationDecimals))
	if err != nil {
		return defaultLocationDecimals
	}
	return clampDecimals(n)
}

// checkLocation refuses a send body whose location cannot be taken, and
// rounds one that can to the person's setting, before anything is written.
// A body with no location is left exactly as it came.
func (r *Runner) checkLocation(ctx context.Context, s *tenant.Store, body *SendBody) error {
	loc := body.Location
	if loc == nil {
		return nil
	}
	invalid := func(why string) error { return fmt.Errorf("%w: %s", chat.ErrInvalid, why) }
	if err := r.setup(); err != nil {
		return err
	}
	switch {
	case r.setting(ctx, s, KeyLocationShare) != "true":
		return invalid("sharing a location with a message is switched off in Settings")
	case body.RetryOf != 0:
		return invalid("a retry sends no message, so it carries no location: leave it out")
	case math.IsNaN(loc.Latitude) || math.IsNaN(loc.Longitude) || math.Abs(loc.Latitude) > 90 || math.Abs(loc.Longitude) > 180:
		return invalid("a location is a latitude from -90 to 90 and a longitude from -180 to 180")
	}
	d := r.locationDecimals(ctx, s)
	body.Location = &Location{Latitude: roundTo(loc.Latitude, d), Longitude: roundTo(loc.Longitude, d)}
	return nil
}

// keepLocation writes the location a turn's message was sent with beside
// that message, with the line its model calls will carry, and returns it.
// It rounds once more to the person's setting as it stands, so a setting
// lowered while the turn waited is kept. A message with no location keeps
// nothing.
func (r *Runner) keepLocation(ctx context.Context, s *tenant.Store, t row, messageID int64) (*SentLocation, error) {
	loc := t.input.Location
	if loc == nil {
		return nil, nil
	}
	d := r.locationDecimals(ctx, s)
	template := r.setting(ctx, s, KeyLocationLine)
	if template == "" {
		if def, ok := settings.Lookup(r.reg, KeyLocationLine); ok {
			template = def.Default
		}
	}
	sent := SentLocation{Latitude: roundTo(loc.Latitude, d), Longitude: roundTo(loc.Longitude, d), Decimals: d}
	sent.Line = locationLine(template, sent.Latitude, sent.Longitude, d)
	if _, err := s.DB().ExecContext(ctx, "INSERT INTO message_locations(message_id, latitude, longitude, decimals, line) VALUES(?, ?, ?, ?, ?)",
		messageID, sent.Latitude, sent.Longitude, sent.Decimals, sent.Line); err != nil {
		return nil, err
	}
	return &sent, nil
}

// withLocations adds to what turns recorded about a chat's messages the
// location each of the person's messages was sent with.
func withLocations(ctx context.Context, s *tenant.Store, chatID string, metas map[int64]meta) error {
	rows, err := s.DB().QueryContext(ctx, `SELECT l.message_id, l.latitude, l.longitude, l.decimals, l.line
		FROM message_locations l JOIN messages m ON m.id = l.message_id WHERE m.chat_id = ?`, chatID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var loc SentLocation
		if err := rows.Scan(&id, &loc.Latitude, &loc.Longitude, &loc.Decimals, &loc.Line); err != nil {
			return err
		}
		m := metas[id]
		m.location = &loc
		metas[id] = m
	}
	return rows.Err()
}

// locationOf is the location a message was sent with, or nil.
func locationOf(ctx context.Context, s *tenant.Store, messageID int64) (*SentLocation, error) {
	var loc SentLocation
	err := s.DB().QueryRowContext(ctx, "SELECT latitude, longitude, decimals, line FROM message_locations WHERE message_id = ?", messageID).
		Scan(&loc.Latitude, &loc.Longitude, &loc.Decimals, &loc.Line)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &loc, nil
}
