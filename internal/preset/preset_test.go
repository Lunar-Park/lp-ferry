package preset

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Lunar-Park/lp-ferry/internal/ledger"
	"github.com/Lunar-Park/lp-ferry/internal/metadata"
)

// The glob dialect is fixed by spec §6.1 / R20; these pin it.
func TestGlobDialect(t *testing.T) {
	cases := []struct {
		pat, rel string
		want     bool
	}{
		{"DCIM/*", "DCIM/100MEDIA/A001.MOV", true}, // * spans separators
		{"*.mov", "a/b/c/clip.MOV", true},          // case-insensitive, any depth
		{"dcim", "DCIM/a.jpg", false},              // naming a dir does not claim children
		{`\Proj\`, "Proj", true},                   // backslash is a separator; edges trimmed
		{"C000?.MP4", "C0001.MP4", true},
		{"C000[12].MP4", "C0003.MP4", false},
		{"**/x", "a/x", true}, // no distinct **
		{"{a,b}", "a", false}, // no braces
	}
	for _, c := range cases {
		if got := GlobMatch(c.pat, c.rel); got != c.want {
			t.Errorf("GlobMatch(%q, %q) = %v", c.pat, c.rel, got)
		}
	}
}

func TestTemplateValidation(t *testing.T) {
	if err := ValidateTemplate("{camera_role}/{capture_date|mtime_date}/{seq:04d}_{filename}", FileTokens); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"", "/abs/{filename}", "a/../{filename}", "{nope}", "{filename", "a\\b"} {
		if ValidateTemplate(bad, FileTokens) == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	for _, tok := range []string{"{ext}", "{stem}", "{category}", "{year}", "{capture_date}"} {
		if ValidateTemplate("Collections/"+tok, GroupTokens) == nil {
			t.Errorf("group template accepted %s", tok)
		}
	}
	if Total("{capture_date}/{filename}") || !Total("{capture_date|mtime_date}/{filename}") {
		t.Fatal("Total wrong")
	}
	p := *Builtins()[BuiltinCategory]
	p.Fallback = "{capture_date}/{filename}"
	if err := p.Validate(); err == nil || !strings.Contains(err.Error(), "fallback") {
		t.Fatalf("a fallback that can fail to render must be refused: %v", err)
	}
	p = *Builtins()[BuiltinCategory]
	p.Exclusions = []Exclusion{{ID: "x", Reason: ""}}
	if err := p.Validate(); err == nil {
		t.Fatal("condition-free, reason-free exclusion accepted")
	}
}

func route(p *Preset, label string, entries []Entry) map[string]Routing {
	out := map[string]Routing{}
	for _, r := range Evaluate(p, label, entries) {
		out[r.Rel] = r
	}
	return out
}

func files(rels ...string) []Entry {
	var es []Entry
	for _, r := range rels {
		es = append(es, Entry{Rel: r, IsDir: strings.HasSuffix(r, "/"), MtimeNS: 1})
	}
	for i := range es {
		es[i].Rel = strings.TrimSuffix(es[i].Rel, "/")
	}
	return es
}

// ORGANIZATION-PRESET-EXAMPLES §1 and §2, verbatim.
func TestExampleTables(t *testing.T) {
	in := files("Photos/Trip/IMG_001.JPG", "Projects/Interview/edit.drp", "Projects/Interview/Media/A001.mov",
		"Documents/notes.pdf", "misc/file.custom", "Empty/")
	got := route(Builtins()[BuiltinPreserve], "Drive-A", in)
	want := map[string]string{
		"Photos/Trip/IMG_001.JPG":           "Sources/Drive-A/Photos/Trip/IMG_001.JPG",
		"Projects/Interview/edit.drp":       "Sources/Drive-A/Projects/Interview/edit.drp",
		"Projects/Interview/Media/A001.mov": "Sources/Drive-A/Projects/Interview/Media/A001.mov",
		"Documents/notes.pdf":               "Sources/Drive-A/Documents/notes.pdf",
		"misc/file.custom":                  "Sources/Drive-A/misc/file.custom",
		"Empty":                             "Sources/Drive-A/Empty",
	}
	for k, v := range want {
		if got[k].DestRel != v {
			t.Errorf("preserve %s -> %s, want %s", k, got[k].DestRel, v)
		}
	}
	in = files("Exports/trailer.mov", "Recordings/interview.wav", "Photos/Trip/IMG_001.JPG", "Admin/notes.pdf",
		"Backups/export.zip", "misc/file.custom", "misc/README", "Empty/")
	got = route(Builtins()[BuiltinCategory], "Drive-A", in)
	want = map[string]string{
		"Exports/trailer.mov":      "Video/Drive-A/Exports/trailer.mov",
		"Recordings/interview.wav": "Audio/Drive-A/Recordings/interview.wav",
		"Photos/Trip/IMG_001.JPG":  "Images/Drive-A/Photos/Trip/IMG_001.JPG",
		"Admin/notes.pdf":          "Documents/Drive-A/Admin/notes.pdf",
		"Backups/export.zip":       "Archives/Drive-A/Backups/export.zip",
		"misc/file.custom":         "Unsorted/Drive-A/misc/file.custom",
		"misc/README":              "Unsorted/Drive-A/misc/README",
		"Empty":                    "Unsorted/Drive-A/Empty",
	}
	for k, v := range want {
		if got[k].DestRel != v {
			t.Errorf("category %s -> %s, want %s", k, got[k].DestRel, v)
		}
	}
}

// Examples §3: keep-together groups beat category rules; outermost wins.
func TestGroupsAndExclusions(t *testing.T) {
	p := *Builtins()[BuiltinCategory]
	p.Groups = []Group{{ID: "keep", When: Conditions{PathGlob: []string{"Photos/Trip", "Music/Album", "Photos/Trip/Inner"}},
		Template: "Collections/{source_label}/{relative_dir}/{filename}"}}
	p.Exclusions = []Exclusion{{ID: "tmp", When: Conditions{Ext: []string{".tmp"}}, Reason: "temporary files"},
		{ID: "cache", When: Conditions{PathGlob: []string{"Cache"}}, Reason: "regenerable cache"}}
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
	in := files("Photos/", "Photos/Trip/", "Photos/Trip/IMG_001.JPG", "Photos/Trip/IMG_001.xmp", "Photos/Trip/notes.txt",
		"Photos/Trip/Empty/", "Photos/Trip/Inner/", "Photos/Trip/Inner/x.wav", "Photos/Trip/scratch.tmp",
		"Music/", "Music/Album/", "Music/Album/track.wav", "Loose/trailer.mov", "Cache/", "Cache/a.bin")
	got := route(&p, "Drive-A", in)
	want := map[string]string{
		"Photos/Trip":             "Collections/Drive-A/Photos/Trip",
		"Photos/Trip/IMG_001.JPG": "Collections/Drive-A/Photos/Trip/IMG_001.JPG",
		"Photos/Trip/IMG_001.xmp": "Collections/Drive-A/Photos/Trip/IMG_001.xmp",
		"Photos/Trip/notes.txt":   "Collections/Drive-A/Photos/Trip/notes.txt",
		"Photos/Trip/Empty":       "Collections/Drive-A/Photos/Trip/Empty",
		"Photos/Trip/Inner/x.wav": "Collections/Drive-A/Photos/Trip/Inner/x.wav",
		"Music/Album/track.wav":   "Collections/Drive-A/Music/Album/track.wav",
		"Loose/trailer.mov":       "Video/Drive-A/Loose/trailer.mov",
	}
	for k, v := range want {
		if got[k].DestRel != v {
			t.Errorf("%s -> %s, want %s", k, got[k].DestRel, v)
		}
	}
	if got["Photos/Trip/Inner/x.wav"].GroupID != "keep@Photos/Trip" {
		t.Fatalf("outer group must win: %+v", got["Photos/Trip/Inner/x.wav"])
	}
	if r := got["Photos/Trip/scratch.tmp"]; r.Action != RouteExclude || r.ExclusionReason != "temporary files" || r.Warning == "" {
		t.Fatalf("exclusion inside a group: %+v", r)
	}
	if r := got["Cache/a.bin"]; r.Action != RouteExclude || r.Rule != "exclusion:cache" {
		t.Fatalf("descendant inherits a directory exclusion: %+v", r)
	}
}

// T2.3 (engine half) — a {capture_date} template never fabricates a date.
func TestMissingCaptureDateRoutesToFallback(t *testing.T) {
	p := &Preset{Schema: Schema, Name: "dated", ConflictPolicy: KeepBoth,
		Rules:    []Rule{{ID: "dated", When: Conditions{Category: []string{CatVideo}}, Template: "{capture_date}/{filename}"}},
		Fallback: "Unsorted/{source_label}/{relative_dir}/{filename}"}
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
	mt := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC).UnixNano()
	es := []Entry{
		{Rel: "A.mov", MtimeNS: mt, Media: metadata.Media{CaptureDate: "2025-12-31"}},
		{Rel: "B.mov", MtimeNS: mt},
	}
	got := route(p, "card", es)
	if got["A.mov"].DestRel != "2025-12-31/A.mov" {
		t.Fatalf("%+v", got["A.mov"])
	}
	b := got["B.mov"]
	today := time.Now().UTC().Format("2006-01-02")
	if b.DestRel != "Unsorted/card/B.mov" || b.Rule != "fallback" || !strings.Contains(b.Warning, "capture_date") || strings.Contains(b.DestRel, today) {
		t.Fatalf("missing date must fall back visibly: %+v", b)
	}
	// Opt-in alternative: modification date, labeled as such.
	p.Rules[0].Template = "{capture_date|mtime_date}/{filename}"
	if got := route(p, "card", es); got["B.mov"].DestRel != "2026-01-02/B.mov" {
		t.Fatalf("%+v", got["B.mov"])
	}
}

func TestSeqIsDeterministic(t *testing.T) {
	p := &Preset{Schema: Schema, Name: "seq", ConflictPolicy: KeepBoth,
		Rules:    []Rule{{ID: "r", Template: "Shoot/{seq:03d}{ext}"}},
		Fallback: "x/{filename}"}
	got := route(p, "s", files("b/z.mov", "a/y.mov", "a/x.mov"))
	if got["a/x.mov"].DestRel != "Shoot/001.mov" || got["a/y.mov"].DestRel != "Shoot/002.mov" || got["b/z.mov"].DestRel != "Shoot/003.mov" {
		t.Fatalf("%+v", got)
	}
}

func TestParseRejectsUnknownAndAbsolute(t *testing.T) {
	good, err := Builtins()[BuiltinCamera].Export()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Parse(good); err != nil {
		t.Fatal(err)
	}
	bad := strings.Replace(string(good), `"schema"`, `"volume_uuid":"X","schema"`, 1)
	if _, err := Parse([]byte(bad)); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unknown field accepted: %v", err)
	}
	p := *Builtins()[BuiltinPreserve]
	p.Groups = []Group{{ID: "g", When: Conditions{PathGlob: []string{"/Volumes/NAS/Proj"}}, Template: "C/{filename}"}}
	if err := p.Validate(); err == nil {
		t.Fatal("absolute glob accepted")
	}
}

func TestServiceRevisionsAreImmutable(t *testing.T) {
	db, err := ledger.Open(filepath.Join(t.TempDir(), "f.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	s := &Service{DB: db}
	if err := s.EnsureBuiltins(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.EnsureBuiltins(ctx); err != nil {
		t.Fatal("EnsureBuiltins must be idempotent:", err)
	}
	list, _ := s.List(ctx)
	if len(list) != 4 {
		t.Fatalf("%+v", list)
	}
	r1, err := s.Create(ctx, Builtins()[BuiltinCategory])
	if err != nil {
		t.Fatal(err)
	}
	edited := *r1.Preset
	edited.Fallback = "Other/{source_label}/{relative_dir}/{filename}"
	r2, err := s.Update(ctx, r1.PresetID, &edited)
	if err != nil || r2.Revision != 2 || r2.Hash == r1.Hash {
		t.Fatalf("%+v %v", r2, err)
	}
	old, err := s.Get(ctx, r1.PresetID, 1)
	if err != nil || old.Preset.Fallback != Builtins()[BuiltinCategory].Fallback {
		t.Fatalf("revision 1 changed: %+v %v", old, err)
	}
	data, _ := s.Export(ctx, r1.PresetID, 2)
	imp, err := s.Import(ctx, data)
	if err != nil || imp.PresetID == r1.PresetID || imp.Hash != r2.Hash {
		t.Fatalf("import must create a new identity with the same content: %+v %v", imp, err)
	}
}

func TestMirrorSourceKeepsTheTree(t *testing.T) {
	got := route(Builtins()[BuiltinMirror], "Drive-A", files("DCIM/100MEDIA/A001.mov", "notes.txt", "Empty/"))
	for rel, want := range map[string]string{"DCIM/100MEDIA/A001.mov": "DCIM/100MEDIA/A001.mov", "notes.txt": "notes.txt", "Empty": "Empty"} {
		if got[rel].DestRel != want {
			t.Errorf("%s -> %s", rel, got[rel].DestRel)
		}
	}
}
