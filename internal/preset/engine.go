package preset

import (
	"errors"
	"fmt"
	"path"
	"sort"
	"strconv"
	"strings"

	"github.com/Lunar-Park/lp-ferry/internal/metadata"
)

// Entry is one source item the engine routes.
type Entry struct {
	Rel        string
	IsDir      bool
	Size       int64
	MtimeNS    int64
	Media      metadata.Media
	CameraRole string
	Companion  bool
}

// Routing actions.
const (
	RouteCopy        = "copy"
	RouteDir         = "dir"
	RouteExclude     = "exclude"
	RouteNeedsReview = "needs_review"
)

// Routing is the engine's decision for one entry.
type Routing struct {
	Rel             string
	IsDir           bool
	Action          string
	DestRel         string
	Rule            string // rule id, "fallback", "empty_dirs", "group:<id>", "exclusion:<id>"
	GroupID         string // stable per source: "<group id>@<root rel>"
	GroupRoot       string // source rel of the group root
	GroupDest       string // rendered destination of the group root
	ExclusionReason string
	Warning         string
}

func anyFold(vals []string, v string) bool {
	for _, x := range vals {
		if strings.EqualFold(x, v) {
			return true
		}
	}
	return false
}

func (c Conditions) matches(label string, e Entry) bool {
	if len(c.SourceLabel) > 0 && !anyFold(c.SourceLabel, label) {
		return false
	}
	if len(c.PathGlob) > 0 {
		ok := false
		for _, g := range c.PathGlob {
			if GlobMatch(g, e.Rel) {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}
	fileOnly := len(c.Ext) > 0 || len(c.Category) > 0 || len(c.CameraRole) > 0 || c.Companion != nil
	if fileOnly && e.IsDir {
		return false
	}
	if len(c.Ext) > 0 && !anyFold(c.Ext, path.Ext(e.Rel)) {
		return false
	}
	if len(c.Category) > 0 && !anyFold(c.Category, CategoryOf(e.Rel)) {
		return false
	}
	if len(c.CameraRole) > 0 && !anyFold(c.CameraRole, e.CameraRole) {
		return false
	}
	if c.Companion != nil && *c.Companion != e.Companion {
		return false
	}
	return true
}

func within(rel, root string) bool { return rel == root || strings.HasPrefix(rel, root+"/") }

// DefaultEmptyDirs is used when a preset names no empty-directory template.
const DefaultEmptyDirs = "Unsorted/{source_label}/{relative_dir}/{filename}"

// Evaluate routes every entry of one source, deterministically:
// exclusions first (everywhere, inherited by descendants), then
// keep-together groups (outermost wins, evaluated once at the root), then
// ordered rules (first match wins), then the fallback. A rule whose
// template needs metadata the file lacks routes to the fallback with a
// warning naming what was missing.
func Evaluate(p *Preset, label string, entries []Entry) []Routing {
	sorted := append([]Entry(nil), entries...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Rel < sorted[j].Rel })
	out := make([]Routing, len(sorted))

	// 1. Exclusions, with directory inheritance.
	type excl struct{ root, id, reason string }
	var exclDirs []excl
	excluded := make([]*Exclusion, len(sorted))
	for i, e := range sorted {
		for _, d := range exclDirs {
			if within(e.Rel, d.root) {
				excluded[i] = &Exclusion{ID: d.id, Reason: d.reason}
				break
			}
		}
		if excluded[i] != nil {
			continue
		}
		for xi := range p.Exclusions {
			if p.Exclusions[xi].When.matches(label, e) {
				excluded[i] = &p.Exclusions[xi]
				if e.IsDir {
					exclDirs = append(exclDirs, excl{e.Rel, p.Exclusions[xi].ID, p.Exclusions[xi].Reason})
				}
				break
			}
		}
	}

	// 2. Group roots: shallowest matching directory; rule order breaks ties.
	type groupRoot struct {
		rel  string
		g    *Group
		dest string
		err  error
	}
	var roots []groupRoot
	dirs := make([]Entry, 0)
	for _, e := range sorted {
		if e.IsDir {
			dirs = append(dirs, e)
		}
	}
	sort.SliceStable(dirs, func(i, j int) bool { return strings.Count(dirs[i].Rel, "/") < strings.Count(dirs[j].Rel, "/") })
	for _, d := range dirs {
		inside := false
		for _, r := range roots {
			if within(d.Rel, r.rel) {
				inside = true
				break
			}
		}
		if inside {
			continue
		}
		for gi := range p.Groups {
			g := &p.Groups[gi]
			if g.When.matches(label, d) {
				dest, err := Render(g.Template, Vars{SourceLabel: label, RelPath: d.Rel, IsDir: true})
				roots = append(roots, groupRoot{rel: d.Rel, g: g, dest: dest, err: err})
				break
			}
		}
	}
	groupOf := func(rel string) *groupRoot {
		for i := range roots {
			if within(rel, roots[i].rel) {
				return &roots[i]
			}
		}
		return nil
	}

	for i, e := range sorted {
		r := Routing{Rel: e.Rel, IsDir: e.IsDir}
		gr := groupOf(e.Rel)
		if gr != nil {
			r.GroupID = gr.g.ID + "@" + gr.rel
			r.GroupRoot, r.GroupDest = gr.rel, gr.dest
		}
		if x := excluded[i]; x != nil {
			r.Action, r.Rule, r.ExclusionReason = RouteExclude, "exclusion:"+x.ID, x.Reason
			if gr != nil {
				r.Warning = "excluded from keep-together group " + gr.g.ID
			}
			out[i] = r
			continue
		}
		if gr != nil {
			if gr.err != nil {
				r.Action, r.Rule, r.Warning = RouteNeedsReview, "group:"+gr.g.ID, "group destination could not render: "+gr.err.Error()
				out[i] = r
				continue
			}
			r.Rule = "group:" + gr.g.ID
			r.DestRel = gr.dest
			if e.Rel != gr.rel {
				r.DestRel = gr.dest + "/" + strings.TrimPrefix(e.Rel, gr.rel+"/")
			}
			r.Action = RouteCopy
			if e.IsDir {
				r.Action = RouteDir
			}
			out[i] = r
			continue
		}
		vars := Vars{SourceLabel: label, RelPath: e.Rel, IsDir: e.IsDir, MtimeNS: e.MtimeNS, Media: e.Media, CameraRole: e.CameraRole}
		if e.IsDir {
			tmpl := p.EmptyDirs
			if tmpl == "" {
				tmpl = DefaultEmptyDirs
			}
			dest, err := Render(tmpl, vars)
			r.Action, r.Rule, r.DestRel = RouteDir, "empty_dirs", dest
			if err != nil {
				r.Action, r.Warning = RouteNeedsReview, err.Error()
			}
			out[i] = r
			continue
		}
		var missing []string
		routed := false
		for ri := range p.Rules {
			rule := &p.Rules[ri]
			if !rule.When.matches(label, e) {
				continue
			}
			dest, err := Render(rule.Template, vars)
			var me *MissingError
			if errors.As(err, &me) {
				// Honest missing-metadata rule: never fabricate; fall back
				// visibly instead.
				missing = me.Tokens
				r.Warning = fmt.Sprintf("rule %s needs %s, which this file does not carry; routed to fallback", rule.ID, strings.Join(me.Tokens, ", "))
				break
			}
			if err != nil {
				r.Action, r.Rule, r.Warning = RouteNeedsReview, rule.ID, err.Error()
				routed = true
				break
			}
			r.Action, r.Rule, r.DestRel = RouteCopy, rule.ID, dest
			routed = true
			break
		}
		if !routed {
			dest, err := Render(p.Fallback, vars)
			r.Rule = "fallback"
			if err != nil {
				r.Action = RouteNeedsReview
				r.Warning = strings.TrimSpace(r.Warning + "; fallback could not render: " + err.Error())
			} else {
				r.Action, r.DestRel = RouteCopy, dest
			}
			_ = missing
		}
		out[i] = r
	}

	numberSeq(out)
	return out
}

// numberSeq replaces seq markers with a deterministic sequence: entries
// sharing one rendered pattern are numbered in source-path order.
func numberSeq(rs []Routing) {
	groups := map[string][]int{}
	var keys []string
	for i, r := range rs {
		if strings.Contains(r.DestRel, seqMarker) {
			k := r.Rule + "\x01" + r.DestRel
			if _, ok := groups[k]; !ok {
				keys = append(keys, k)
			}
			groups[k] = append(groups[k], i)
		}
	}
	// Entries whose pattern differs only by filename share a folder count.
	byDir := map[string][]int{}
	var dirKeys []string
	for _, k := range keys {
		for _, i := range groups[k] {
			d := rs[i].Rule + "\x01" + path.Dir(strings.ReplaceAll(rs[i].DestRel, seqMarker, ""))
			if _, ok := byDir[d]; !ok {
				dirKeys = append(dirKeys, d)
			}
			byDir[d] = append(byDir[d], i)
		}
	}
	for _, d := range dirKeys {
		idx := byDir[d]
		sort.Slice(idx, func(a, b int) bool { return rs[idx[a]].Rel < rs[idx[b]].Rel })
		for n, i := range idx {
			rs[i].DestRel = replaceSeq(rs[i].DestRel, n+1)
		}
	}
}

func replaceSeq(s string, n int) string {
	for {
		i := strings.Index(s, seqMarker)
		if i < 0 {
			return s
		}
		rest := s[i+len(seqMarker):]
		j := strings.IndexByte(rest, 0)
		width, _ := strconv.Atoi(rest[:j])
		s = s[:i] + fmt.Sprintf("%0*d", width, n) + rest[j+1:]
	}
}
