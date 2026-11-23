package preset

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/Lunar-Park/lp-ferry/internal/ledger"
)

// Revision is one stored, immutable preset revision.
type Revision struct {
	PresetID  string  `json:"preset_id"`
	Revision  int     `json:"revision"`
	Hash      string  `json:"content_hash"`
	CreatedAt string  `json:"created_at"`
	Preset    *Preset `json:"preset"`
}

// Summary lists a preset and its latest revision.
type Summary struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Latest   int    `json:"latest_revision"`
	Archived bool   `json:"archived"`
	Builtin  bool   `json:"builtin"`
}

// Service stores presets as immutable revisions.
type Service struct{ DB *ledger.DB }

func newPresetID() string {
	var b [6]byte
	_, _ = rand.Read(b[:])
	return "prs_" + hex.EncodeToString(b[:])
}

// EnsureBuiltins installs the starter presets as revision 1 if absent.
func (s *Service) EnsureBuiltins(ctx context.Context) error {
	return s.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		ids := make([]string, 0)
		b := Builtins()
		for id := range b {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		for _, id := range ids {
			var n int
			if err := tx.QueryRow(`SELECT COUNT(*) FROM presets WHERE id = ?`, id).Scan(&n); err != nil {
				return err
			}
			if n == 0 {
				if err := insertRevision(tx, id, 1, b[id], true); err != nil {
					return err
				}
			}
		}
		return nil
	})
}

func insertRevision(tx *sql.Tx, id string, rev int, p *Preset, create bool) error {
	now := ledger.Now()
	if create {
		if _, err := tx.Exec(`INSERT INTO presets (id, name, created_at, updated_at) VALUES (?,?,?,?)`, id, p.Name, now, now); err != nil {
			return err
		}
	} else if _, err := tx.Exec(`UPDATE presets SET name = ?, updated_at = ? WHERE id = ?`, p.Name, now, id); err != nil {
		return err
	}
	rules, _ := json.Marshal(p.Rules)
	groups, _ := json.Marshal(p.Groups)
	excl, _ := json.Marshal(p.Exclusions)
	_, err := tx.Exec(`INSERT INTO preset_revisions (preset_id, revision, name, description, rules_json, groups_json,
		exclusions_json, fallback_template, conflict_policy, content_hash, created_at) VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
		id, rev, p.Name, p.Description, string(rules), string(groups), string(excl), p.Fallback+"\x00"+p.EmptyDirs,
		p.ConflictPolicy, p.Hash(), now)
	return err
}

// Create stores a new preset at revision 1.
func (s *Service) Create(ctx context.Context, p *Preset) (*Revision, error) {
	if err := p.Validate(); err != nil {
		return nil, err
	}
	id := newPresetID()
	if err := s.DB.WriteTx(ctx, func(tx *sql.Tx) error { return insertRevision(tx, id, 1, p, true) }); err != nil {
		return nil, err
	}
	return s.Get(ctx, id, 0)
}

// Update appends revision N+1. Pending plans built on this preset are
// invalidated in the same transaction; destinations stay pinned to the
// revision they chose until explicitly updated.
func (s *Service) Update(ctx context.Context, id string, p *Preset) (*Revision, error) {
	if err := p.Validate(); err != nil {
		return nil, err
	}
	if err := s.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		var latest int
		if err := tx.QueryRow(`SELECT COALESCE(MAX(revision),0) FROM preset_revisions WHERE preset_id = ?`, id).Scan(&latest); err != nil {
			return err
		}
		if latest == 0 {
			return fmt.Errorf("preset %s: %w", id, ledger.ErrNotFound)
		}
		if err := insertRevision(tx, id, latest+1, p, false); err != nil {
			return err
		}
		_, err := ledger.InvalidatePlansForPreset(tx, id, fmt.Sprintf("preset %s edited (revision %d)", id, latest+1))
		return err
	}); err != nil {
		return nil, err
	}
	return s.Get(ctx, id, 0)
}

// Get loads a revision (rev 0 = latest).
func (s *Service) Get(ctx context.Context, id string, rev int) (*Revision, error) {
	q := `SELECT revision, name, description, rules_json, groups_json, exclusions_json, fallback_template, conflict_policy,
		content_hash, created_at FROM preset_revisions WHERE preset_id = ?`
	args := []any{id}
	if rev > 0 {
		q += ` AND revision = ?`
		args = append(args, rev)
	} else {
		q += ` ORDER BY revision DESC LIMIT 1`
	}
	var r Revision
	var rules, groups, excl, fallback string
	p := &Preset{Schema: Schema}
	err := s.DB.Read().QueryRowContext(ctx, q, args...).Scan(&r.Revision, &p.Name, &p.Description, &rules, &groups, &excl,
		&fallback, &p.ConflictPolicy, &r.Hash, &r.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("preset %s revision %d: %w", id, rev, ledger.ErrNotFound)
	}
	if err != nil {
		return nil, err
	}
	if err := errors.Join(json.Unmarshal([]byte(rules), &p.Rules), json.Unmarshal([]byte(groups), &p.Groups),
		json.Unmarshal([]byte(excl), &p.Exclusions)); err != nil {
		return nil, err
	}
	p.Fallback, p.EmptyDirs = splitFallback(fallback)
	if p.Hash() != r.Hash {
		return nil, fmt.Errorf("preset %s revision %d fails its content hash; the stored revision was altered", id, r.Revision)
	}
	r.PresetID, r.Preset = id, p
	return &r, nil
}

func splitFallback(s string) (string, string) {
	for i := 0; i < len(s); i++ {
		if s[i] == 0 {
			return s[:i], s[i+1:]
		}
	}
	return s, ""
}

// List returns every preset with its latest revision.
func (s *Service) List(ctx context.Context) ([]Summary, error) {
	rows, err := s.DB.Read().QueryContext(ctx, `SELECT p.id, p.name, p.archived, MAX(r.revision) FROM presets p
		JOIN preset_revisions r ON r.preset_id = p.id GROUP BY p.id ORDER BY p.name`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	b := Builtins()
	var out []Summary
	for rows.Next() {
		var sm Summary
		if err := rows.Scan(&sm.ID, &sm.Name, &sm.Archived, &sm.Latest); err != nil {
			return nil, err
		}
		_, sm.Builtin = b[sm.ID]
		out = append(out, sm)
	}
	return out, rows.Err()
}

// Import creates a new local preset from portable JSON. It never
// overwrites an existing preset.
func (s *Service) Import(ctx context.Context, data []byte) (*Revision, error) {
	p, err := Parse(data)
	if err != nil {
		return nil, err
	}
	return s.Create(ctx, p)
}

// Export renders a revision as portable JSON.
func (s *Service) Export(ctx context.Context, id string, rev int) ([]byte, error) {
	r, err := s.Get(ctx, id, rev)
	if err != nil {
		return nil, err
	}
	return r.Preset.Export()
}
