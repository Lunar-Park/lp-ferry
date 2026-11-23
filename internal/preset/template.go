package preset

import (
	"errors"
	"fmt"
	"path"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Lunar-Park/lp-ferry/internal/metadata"
)

// Token sets.
var (
	// FileTokens may appear in file rules and the fallback.
	FileTokens = tokenSet("source_label", "relative_dir", "filename", "stem", "ext", "category",
		"year", "month", "mtime_date", "mtime_year", "mtime_month",
		"capture_date", "capture_year", "capture_month",
		"camera_role", "camera_model", "reel", "resolution_bucket", "seq")
	// GroupTokens may appear in group destinations (evaluated once, for the
	// group root directory) and the empty-directory template.
	GroupTokens = tokenSet("source_label", "relative_dir", "filename")
	// alwaysAvailable never render missing.
	alwaysAvailable = tokenSet("source_label", "relative_dir", "filename", "stem", "ext", "category",
		"year", "month", "mtime_date", "mtime_year", "mtime_month", "seq")
)

func tokenSet(names ...string) map[string]bool {
	m := map[string]bool{}
	for _, n := range names {
		m[n] = true
	}
	return m
}

var (
	tokenRe = regexp.MustCompile(`\{([^{}]*)\}`)
	seqRe   = regexp.MustCompile(`^seq(?::0(\d)d)?$`)
)

// alternatives parses "{a|b|c}" -> [a b c] with seq format stripped.
func alternatives(body string) []string { return strings.Split(body, "|") }

func baseName(tok string) string {
	if seqRe.MatchString(tok) {
		return "seq"
	}
	return tok
}

// ValidateTemplate checks syntax, token names and literal safety.
func ValidateTemplate(t string, allowed map[string]bool) error {
	if strings.TrimSpace(t) == "" {
		return errors.New("template is empty")
	}
	if strings.ContainsRune(t, 0) || strings.ContainsRune(t, '\\') {
		return errors.New("template contains NUL or backslash")
	}
	if strings.HasPrefix(t, "/") {
		return errors.New("template must be relative")
	}
	stripped := tokenRe.ReplaceAllString(t, "x")
	if strings.ContainsAny(stripped, "{}") {
		return errors.New("unbalanced braces")
	}
	for _, comp := range strings.Split(stripped, "/") {
		if comp == ".." || comp == "." {
			return fmt.Errorf("template component %q is not allowed", comp)
		}
	}
	for _, m := range tokenRe.FindAllStringSubmatch(t, -1) {
		for _, alt := range alternatives(m[1]) {
			if !allowed[baseName(alt)] {
				return fmt.Errorf("token {%s} is not allowed here", alt)
			}
		}
	}
	return nil
}

// Total reports whether every token in t always renders (each alternative
// chain ends in an always-available token).
func Total(t string) bool {
	for _, m := range tokenRe.FindAllStringSubmatch(t, -1) {
		alts := alternatives(m[1])
		if !alwaysAvailable[baseName(alts[len(alts)-1])] {
			return false
		}
	}
	return true
}

// Vars are the values a template can draw on for one entry.
type Vars struct {
	SourceLabel string
	RelPath     string // source-relative path ("/" separated)
	IsDir       bool
	MtimeNS     int64
	Media       metadata.Media
	CameraRole  string
	// Seq is filled in by the engine's second pass.
	Seq int
}

func (v Vars) value(tok string) (string, bool) {
	dir, file := path.Split(v.RelPath)
	dir = strings.TrimSuffix(dir, "/")
	ext := ""
	if !v.IsDir {
		ext = path.Ext(file)
	}
	mt := time.Unix(0, v.MtimeNS).UTC()
	hasMtime := v.MtimeNS > 0
	var cap time.Time
	hasCap := false
	if v.Media.CaptureDate != "" {
		if t, err := time.Parse("2006-01-02", v.Media.CaptureDate); err == nil {
			cap, hasCap = t, true
		}
	}
	switch tok {
	case "source_label":
		return v.SourceLabel, v.SourceLabel != ""
	case "relative_dir":
		return dir, true
	case "filename":
		return file, true
	case "stem":
		return strings.TrimSuffix(file, ext), true
	case "ext":
		return ext, true
	case "category":
		return CategoryOf(file), true
	case "year", "mtime_year":
		return mt.Format("2006"), hasMtime
	case "month", "mtime_month":
		return mt.Format("01"), hasMtime
	case "mtime_date":
		return mt.Format("2006-01-02"), hasMtime
	case "capture_date":
		return cap.Format("2006-01-02"), hasCap
	case "capture_year":
		return cap.Format("2006"), hasCap
	case "capture_month":
		return cap.Format("01"), hasCap
	case "camera_role":
		return v.CameraRole, v.CameraRole != ""
	case "camera_model":
		return v.Media.Model, v.Media.Model != ""
	case "reel":
		return v.Media.Reel, v.Media.Reel != ""
	case "resolution_bucket":
		b := v.Media.ResolutionBucket()
		return b, b != ""
	}
	return "", false
}

// seqMarker stands in for {seq} until the second pass numbers it.
const seqMarker = "\x00SEQ\x00"

// MissingError names the metadata a template needed but the entry lacks.
type MissingError struct{ Tokens []string }

func (e *MissingError) Error() string {
	return "missing metadata: " + strings.Join(e.Tokens, ", ")
}

// Render renders t for v. relative_dir expands into components; every
// other value is a single safe component. {seq} renders as seqMarker.
// A token with no value and no available alternative is a *MissingError:
// Ferry never substitutes today, an empty string, or a guess.
func Render(t string, v Vars) (string, error) {
	var missing []string
	out := tokenRe.ReplaceAllStringFunc(t, func(m string) string {
		alts := alternatives(m[1 : len(m)-1])
		for _, alt := range alts {
			if sm := seqRe.FindStringSubmatch(alt); sm != nil {
				width := "1"
				if sm[1] != "" {
					width = sm[1]
				}
				return seqMarker + width + "\x00"
			}
			val, ok := v.value(alt)
			if !ok {
				continue
			}
			if alt == "relative_dir" {
				return val
			}
			return sanitize(val)
		}
		missing = append(missing, alts[0])
		return ""
	})
	if len(missing) > 0 {
		return "", &MissingError{Tokens: missing}
	}
	var parts []string
	for _, c := range strings.Split(out, "/") {
		if c == "" || c == "." {
			continue
		}
		if c == ".." {
			return "", errors.New("rendered path contains '..'")
		}
		parts = append(parts, c)
	}
	if len(parts) == 0 {
		return "", errors.New("rendered to an empty path")
	}
	for _, p := range parts {
		if len(p) > 255 {
			return "", fmt.Errorf("component %q exceeds 255 bytes", p[:32]+"…")
		}
	}
	return strings.Join(parts, "/"), nil
}

// sanitize makes a token value one safe path component without hiding it:
// separators and control bytes become "_"; "." and ".." become "_".
func sanitize(s string) string {
	if !utf8.ValidString(s) {
		s = strings.ToValidUTF8(s, "_")
	}
	s = strings.Map(func(r rune) rune {
		if r == '/' || r == '\\' || r < 0x20 || r == 0x7f {
			return '_'
		}
		return r
	}, strings.TrimSpace(s))
	if s == "." || s == ".." {
		return "_"
	}
	return s
}
