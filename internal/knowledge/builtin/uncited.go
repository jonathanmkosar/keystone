package builtin

import (
	"context"

	"github.com/jonathanmkosar/keystone/internal/knowledge"
	"github.com/jonathanmkosar/keystone/internal/tenant"
)

// Uncited returns the sources no page cites, oldest first, at most limit of
// them, with their text and label: the work of the built-in store's
// compile job (P8-06, internal/wikijobs). A source someone withdrew is
// still returned here; the caller leaves it out, as knowledge.Open's store
// does.
func Uncited(ctx context.Context, t *tenant.Store, limit int) ([]knowledge.Source, error) {
	if t == nil {
		return nil, invalid("the built-in knowledge store was given no tenant")
	}
	if limit < 1 {
		limit = 1
	}
	rows, err := t.DB().QueryContext(ctx, `
		SELECT s.id, s.space, COALESCE(sp.label, 'sensitive'), s.name, s.origin, s.dated, s.text
		FROM knowledge_sources s
		LEFT JOIN knowledge_spaces sp ON sp.name = s.space
		WHERE NOT EXISTS (SELECT 1 FROM knowledge_page_sources ps WHERE ps.source_id = s.id)
		ORDER BY s.id LIMIT ?`, limit)
	if err != nil {
		return nil, fail(ctx, err)
	}
	defer rows.Close()
	var out []knowledge.Source
	for rows.Next() {
		var n int64
		var src knowledge.Source
		var label string
		if err := rows.Scan(&n, &src.Space, &label, &src.Name, &src.Origin, &src.Dated, &src.Text); err != nil {
			return nil, fail(ctx, err)
		}
		src.ID, src.Label = sourceID(n), knowledge.Label(label)
		if src.Label != knowledge.LabelNormal {
			src.Label = knowledge.LabelSensitive
		}
		out = append(out, src)
	}
	if err := rows.Err(); err != nil {
		return nil, fail(ctx, err)
	}
	return out, nil
}

// SpaceLabels returns the label of every space the store holds, pages or
// none: what the compile job (P8-06) checks before it writes a page that
// cites a sensitive source.
func SpaceLabels(ctx context.Context, t *tenant.Store) (map[string]knowledge.Label, error) {
	if t == nil {
		return nil, invalid("the built-in knowledge store was given no tenant")
	}
	rows, err := t.DB().QueryContext(ctx, "SELECT name, label FROM knowledge_spaces")
	if err != nil {
		return nil, fail(ctx, err)
	}
	defer rows.Close()
	out := map[string]knowledge.Label{}
	for rows.Next() {
		var name, label string
		if err := rows.Scan(&name, &label); err != nil {
			return nil, fail(ctx, err)
		}
		out[name] = knowledge.LabelSensitive
		if label == string(knowledge.LabelNormal) {
			out[name] = knowledge.LabelNormal
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fail(ctx, err)
	}
	return out, nil
}
