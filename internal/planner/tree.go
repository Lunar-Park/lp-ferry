package planner

import (
	"context"
	"fmt"
	"path"
	"sort"
	"strconv"
	"strings"

	"github.com/Lunar-Park/lp-ferry/internal/ledger"
	"github.com/Lunar-Park/lp-ferry/internal/model"
)

// Transform is one before → after example shown on a folder.
type Transform struct {
	Before string `json:"before"`
	After  string `json:"after"`
	Action string `json:"action"`
}

// TreeNode is one node of the destination ("After") tree.
type TreeNode struct {
	Name             string      `json:"name"`
	Path             string      `json:"path"`
	Kind             string      `json:"kind"` // dir | file
	FileCount        int64       `json:"file_count"`
	TotalBytes       int64       `json:"total_bytes"`
	ConflictCount    int64       `json:"conflict_count"`
	RenamedCount     int64       `json:"renamed_count"`
	ExcludedCount    int64       `json:"excluded_count,omitempty"`
	MatchedRule      string      `json:"matched_rule,omitempty"`
	SampleTransforms []Transform `json:"sample_transforms,omitempty"`
	// File nodes only.
	Before       string `json:"before,omitempty"`
	Action       string `json:"action,omitempty"`
	ConflictKind string `json:"conflict_kind,omitempty"`
	RenamedFrom  string `json:"renamed_from,omitempty"`
	// Children are paged: ChildCount is the total, NextCursor continues.
	ChildCount int        `json:"child_count"`
	Children   []TreeNode `json:"children,omitempty"`
	NextCursor string     `json:"next_cursor,omitempty"`
}

// TreeQuery asks for one window of the tree.
type TreeQuery struct {
	PlanID     string `json:"plan_id"`
	DestID     string `json:"destination_id"`
	ParentPath string `json:"parent_path"`
	Depth      int    `json:"depth"`
	Limit      int    `json:"limit"`
	Cursor     string `json:"cursor,omitempty"`
}

type treeNode struct {
	name, path string
	dir        bool
	kids       map[string]*treeNode
	sorted     []*treeNode
	files      int64
	bytes      int64
	conflicts  int64
	renamed    int64
	excluded   int64
	rules      map[string]int64
	samples    []Transform
	entry      *model.PlanEntry
}

func newDir(name, p string) *treeNode {
	return &treeNode{name: name, path: p, dir: true, kids: map[string]*treeNode{}, rules: map[string]int64{}}
}

func (n *treeNode) child(name string, dir bool) *treeNode {
	c := n.kids[name]
	if c == nil {
		p := name
		if n.path != "" {
			p = n.path + "/" + name
		}
		if dir {
			c = newDir(name, p)
		} else {
			c = &treeNode{name: name, path: p}
		}
		n.kids[name] = c
		n.sorted = nil
	}
	return c
}

func (n *treeNode) children() []*treeNode {
	if n.sorted == nil {
		n.sorted = make([]*treeNode, 0, len(n.kids))
		for _, c := range n.kids {
			n.sorted = append(n.sorted, c)
		}
		sort.Slice(n.sorted, func(i, j int) bool {
			a, b := n.sorted[i], n.sorted[j]
			if a.dir != b.dir {
				return a.dir
			}
			return a.name < b.name
		})
	}
	return n.sorted
}

func buildTree(entries []model.PlanEntry) *treeNode {
	root := newDir("", "")
	for i := range entries {
		e := &entries[i]
		if e.Action == model.ActionExclude || e.DestRelPath == "" {
			root.excluded++
			continue
		}
		parts := strings.Split(e.DestRelPath, "/")
		chain := []*treeNode{root}
		cur := root
		for _, comp := range parts[:len(parts)-1] {
			cur = cur.child(comp, true)
			chain = append(chain, cur)
		}
		isDir := e.Action == model.ActionDir
		leaf := cur.child(parts[len(parts)-1], isDir)
		if isDir {
			continue
		}
		leaf.entry = e
		renamed := e.RenamedFrom != "" || path.Base(e.DestRelPath) != path.Base(e.SourceRel)
		for _, n := range append(chain, leaf) {
			n.files++
			n.bytes += e.Size
			if e.ConflictKind != "" {
				n.conflicts++
			}
			if renamed {
				n.renamed++
			}
			if n.rules != nil {
				n.rules[e.MatchedRule]++
			}
		}
		if len(cur.samples) < 3 {
			cur.samples = append(cur.samples, Transform{Before: e.SourceLabel + ":" + e.SourceRel, After: e.DestRelPath, Action: e.Action})
		}
	}
	return root
}

func (n *treeNode) render(depth, limit, offset int) TreeNode {
	out := TreeNode{Name: n.name, Path: n.path, Kind: "file", FileCount: n.files, TotalBytes: n.bytes,
		ConflictCount: n.conflicts, RenamedCount: n.renamed, ExcludedCount: n.excluded}
	if n.dir {
		out.Kind = "dir"
		if len(n.rules) == 1 {
			for r := range n.rules {
				out.MatchedRule = r
			}
		}
		out.SampleTransforms = n.samples
		kids := n.children()
		out.ChildCount = len(kids)
		if depth > 0 {
			end := min(offset+limit, len(kids))
			for _, c := range kids[min(offset, len(kids)):end] {
				out.Children = append(out.Children, c.render(depth-1, limit, 0))
			}
			if end < len(kids) {
				out.NextCursor = strconv.Itoa(end)
			}
		}
	} else if e := n.entry; e != nil {
		out.Before, out.Action, out.ConflictKind, out.RenamedFrom = e.SourceLabel+":"+e.SourceRel, e.Action, e.ConflictKind, e.RenamedFrom
		out.MatchedRule = e.MatchedRule
	}
	return out
}

// Tree returns a window of the destination tree for one plan destination.
// Plans are immutable, so the built tree is cached.
func (p *Planner) Tree(ctx context.Context, q TreeQuery) (*TreeNode, error) {
	if q.Limit <= 0 {
		q.Limit = DefaultPageSize
	}
	q.Limit = min(q.Limit, MaxPageSize)
	if q.Depth <= 0 {
		q.Depth = 1
	}
	key := q.PlanID + "\x00" + q.DestID
	p.mu.Lock()
	root := p.trees[key]
	p.mu.Unlock()
	if root == nil {
		var all []model.PlanEntry
		var after int64
		for {
			page, _, err := ledger.ListPlanEntries(ctx, p.DB.Read(), q.PlanID, q.DestID, after, MaxPageSize)
			if err != nil {
				return nil, err
			}
			all = append(all, page...)
			if len(page) < MaxPageSize {
				break
			}
			after = page[len(page)-1].ID
		}
		if len(all) == 0 {
			if _, err := ledger.GetPlan(ctx, p.DB.Read(), q.PlanID); err != nil {
				return nil, err
			}
		}
		root = buildTree(all)
		p.mu.Lock()
		if p.trees == nil || len(p.trees) > 32 {
			p.trees = map[string]*treeNode{}
		}
		p.trees[key] = root
		p.mu.Unlock()
	}
	n := root
	if q.ParentPath != "" {
		for _, comp := range strings.Split(strings.Trim(q.ParentPath, "/"), "/") {
			if n = n.kids[comp]; n == nil || !n.dir {
				return nil, fmt.Errorf("no folder %q in the plan tree", q.ParentPath)
			}
		}
	}
	offset := 0
	if q.Cursor != "" {
		var err error
		if offset, err = strconv.Atoi(q.Cursor); err != nil || offset < 0 {
			return nil, fmt.Errorf("bad cursor %q", q.Cursor)
		}
	}
	out := n.render(q.Depth, q.Limit, offset)
	return &out, nil
}
