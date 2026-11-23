// Package preset is Ferry's organization language: ordered, deterministic
// rules that map source-relative paths to destination-relative paths. No
// expressions, regex or scripting — only conditions and templates, so a
// preset author can predict every placement before a byte moves.
package preset

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"strings"
)

// Schema identifies portable preset JSON.
const Schema = "ferry-preset-v1"

// Conflict policies.
const (
	KeepBoth      = "keep_both"
	SkipIdentical = "skip_identical"
	NeedsReview   = "needs_review"
)

// Conditions: AND across fields, OR within a field. An empty field is
// "any". At least one field must be set where a condition is required.
type Conditions struct {
	PathGlob    []string `json:"path_glob,omitempty"`
	Ext         []string `json:"ext,omitempty"` // ".mov"; "" means extensionless
	Category    []string `json:"category,omitempty"`
	SourceLabel []string `json:"source_label,omitempty"`
	CameraRole  []string `json:"camera_role,omitempty"`
	Companion   *bool    `json:"companion,omitempty"` // a known vendor sidecar of a clip
}

func (c Conditions) empty() bool {
	return len(c.PathGlob) == 0 && len(c.Ext) == 0 && len(c.Category) == 0 && len(c.SourceLabel) == 0 &&
		len(c.CameraRole) == 0 && c.Companion == nil
}

// Rule routes matching files with a template.
type Rule struct {
	ID       string     `json:"id"`
	When     Conditions `json:"when"`
	Template string     `json:"template"`
}

// Group keeps a matching directory subtree together.
type Group struct {
	ID       string     `json:"id"`
	When     Conditions `json:"when"`
	Template string     `json:"template"`
}

// Exclusion leaves matching entries behind, with a stated reason.
type Exclusion struct {
	ID     string     `json:"id"`
	When   Conditions `json:"when"`
	Reason string     `json:"reason"`
}

// Preset is one immutable revision's content.
type Preset struct {
	Schema         string      `json:"schema"`
	Name           string      `json:"name"`
	Description    string      `json:"description,omitempty"`
	Rules          []Rule      `json:"rules"`
	Groups         []Group     `json:"groups,omitempty"`
	Exclusions     []Exclusion `json:"exclusions,omitempty"`
	Fallback       string      `json:"fallback"`
	EmptyDirs      string      `json:"empty_dirs,omitempty"`
	ConflictPolicy string      `json:"conflict_policy"`
}

// FieldError names the field a validation failed on.
type FieldError struct {
	Field string
	Msg   string
}

func (e *FieldError) Error() string { return e.Field + ": " + e.Msg }

// ErrInvalid wraps validation failures.
var ErrInvalid = errors.New("invalid preset")

// Validate checks the whole preset and returns every field error.
func (p *Preset) Validate() error {
	var errs []error
	add := func(field, msg string, args ...any) {
		errs = append(errs, &FieldError{Field: field, Msg: fmt.Sprintf(msg, args...)})
	}
	if p.Schema != Schema {
		add("schema", "want %q", Schema)
	}
	if strings.TrimSpace(p.Name) == "" {
		add("name", "required")
	}
	switch p.ConflictPolicy {
	case KeepBoth, SkipIdentical, NeedsReview:
	default:
		add("conflict_policy", "want keep_both, skip_identical or needs_review")
	}
	ids := map[string]bool{}
	checkID := func(field, id string) {
		if strings.TrimSpace(id) == "" {
			add(field+".id", "required")
		} else if ids[id] {
			add(field+".id", "duplicate id %q", id)
		}
		ids[id] = true
	}
	checkCond := func(field string, c Conditions) {
		for _, g := range c.PathGlob {
			if err := validGlob(g); err != nil {
				add(field+".when.path_glob", "%v", err)
			}
		}
		for _, e := range c.Ext {
			if e != "" && (!strings.HasPrefix(e, ".") || strings.ContainsAny(e, "/\\\x00")) {
				add(field+".when.ext", "%q must start with '.' (or be \"\" for extensionless)", e)
			}
		}
		for _, cat := range c.Category {
			if !ValidCategory(cat) {
				add(field+".when.category", "unknown category %q", cat)
			}
		}
	}
	for i, r := range p.Rules {
		f := fmt.Sprintf("rules[%d]", i)
		checkID(f, r.ID)
		checkCond(f, r.When)
		if err := ValidateTemplate(r.Template, FileTokens); err != nil {
			add(f+".template", "%v", err)
		}
	}
	for i, g := range p.Groups {
		f := fmt.Sprintf("groups[%d]", i)
		checkID(f, g.ID)
		if len(g.When.PathGlob) == 0 {
			add(f+".when.path_glob", "a group claims directories by path_glob")
		}
		if len(g.When.Ext) > 0 || len(g.When.Category) > 0 || len(g.When.CameraRole) > 0 || g.When.Companion != nil {
			add(f+".when", "a group matches directories; only path_glob and source_label apply")
		}
		checkCond(f, g.When)
		if err := ValidateTemplate(g.Template, GroupTokens); err != nil {
			add(f+".template", "%v", err)
		}
	}
	for i, x := range p.Exclusions {
		f := fmt.Sprintf("exclusions[%d]", i)
		checkID(f, x.ID)
		checkCond(f, x.When)
		if x.When.empty() {
			add(f+".when", "an exclusion needs at least one condition")
		}
		if strings.TrimSpace(x.Reason) == "" {
			add(f+".reason", "required: it is the receipt's account of every file left behind")
		}
	}
	if err := ValidateTemplate(p.Fallback, FileTokens); err != nil {
		add("fallback", "%v", err)
	} else if !Total(p.Fallback) {
		add("fallback", "must always render: metadata tokens need an always-available alternative (e.g. {capture_date|mtime_date})")
	}
	if p.EmptyDirs != "" {
		if err := ValidateTemplate(p.EmptyDirs, GroupTokens); err != nil {
			add("empty_dirs", "%v", err)
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("%w: %w", ErrInvalid, errors.Join(errs...))
	}
	return nil
}

// Canonical is the content-addressed form: stable JSON of the content.
func (p *Preset) Canonical() []byte {
	b, _ := json.Marshal(p)
	return b
}

// Hash is SHA-256 of Canonical.
func (p *Preset) Hash() string {
	s := sha256.Sum256(p.Canonical())
	return hex.EncodeToString(s[:])
}

// Parse decodes portable JSON strictly (unknown fields fail) and validates.
// Exported presets hold relative rules only: absolute paths, drive or
// share identities are refused.
func Parse(data []byte) (*Preset, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var p Preset
	if err := dec.Decode(&p); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	if err := p.Validate(); err != nil {
		return nil, err
	}
	return &p, nil
}

// Export renders portable JSON.
func (p *Preset) Export() ([]byte, error) {
	if err := p.Validate(); err != nil {
		return nil, err
	}
	return json.MarshalIndent(p, "", "  ")
}

func validGlob(g string) error {
	if strings.TrimSpace(g) == "" {
		return errors.New("empty glob")
	}
	if strings.HasPrefix(g, "/") || (len(g) > 1 && g[1] == ':') {
		return fmt.Errorf("%q is absolute; presets hold relative rules only", g)
	}
	if _, err := path.Match(strings.ToLower(normGlob(g)), "x"); err != nil {
		return fmt.Errorf("%q: %w", g, err)
	}
	return nil
}
