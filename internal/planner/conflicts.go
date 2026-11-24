package planner

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"

	"golang.org/x/text/unicode/norm"

	"github.com/Lunar-Park/lp-ferry/internal/checksum"
	"github.com/Lunar-Park/lp-ferry/internal/model"
	"github.com/Lunar-Park/lp-ferry/internal/preset"
	"github.com/Lunar-Park/lp-ferry/internal/storage"
)

// Conflict kinds. Each is a distinct finding (spec §6.4).
const (
	ConflictDuplicateTarget   = "duplicate_target"      // two sources render the same path
	ConflictCaseOnly          = "case_only"             // differ only by case
	ConflictUnicode           = "unicode_normalization" // differ only by NFC/NFD
	ConflictExistingFile      = "existing_file"         // something already there (keep_both)
	ConflictDifferentContent  = "same_name_different_content"
	ConflictExistingIdentical = "existing_identical" // full checksum equal: skip
	ConflictFileVsDir         = "file_vs_directory"
	ConflictAncestor          = "ancestor_conflict"
	ConflictUnsafe            = "unsafe_destination_path"
	ConflictGroupRoot         = "group_root_collision"
)

// fold is the conservative comparison key: case-folded NFC. Unknown
// target case sensitivity is treated as insensitive and normalizing.
func fold(s string) string { return strings.ToLower(norm.NFC.String(s)) }

// item is one entry being placed on one destination.
type item struct {
	src   int
	inv   *Inventory
	scan  ScanEntry
	r     preset.Routing
	entry model.PlanEntry
}

type allocator struct {
	ctx      context.Context
	root     *storage.Root // nil when the destination folder does not exist
	policy   string
	algo     string
	files    map[string]string // fold(path) -> claimed file path
	dirs     map[string]bool   // fold(path) of every claimed directory and ancestor
	listings map[string]map[string]string
	findings []model.Finding
	destID   string
	// lister overrides local directory reads (remote destinations).
	lister func(dirRel string) map[string]string
	remote bool
}

func newAllocator(ctx context.Context, root *storage.Root, policy, algo, destID string) *allocator {
	return &allocator{ctx: ctx, root: root, policy: policy, algo: algo, destID: destID,
		files: map[string]string{}, dirs: map[string]bool{}, listings: map[string]map[string]string{}}
}

// listing returns fold(name) -> kind for a destination directory, read
// without following links. A missing directory lists as empty.
func (a *allocator) listing(dirRel string) map[string]string {
	if l, ok := a.listings[dirRel]; ok {
		return l
	}
	if a.lister != nil {
		l := a.lister(dirRel)
		a.listings[dirRel] = l
		return l
	}
	l := map[string]string{}
	a.listings[dirRel] = l
	if a.root == nil {
		return l
	}
	entries, err := os.ReadDir(filepath.Join(a.root.Real, filepath.FromSlash(dirRel)))
	if err != nil {
		return l
	}
	for _, e := range entries {
		kind := TypeOther
		switch t := e.Type(); {
		case t.IsRegular():
			kind = TypeFile
		case t.IsDir():
			kind = TypeDir
		case t&os.ModeSymlink != 0:
			kind = TypeSymlink
		}
		l[fold(e.Name())] = kind + "\x00" + e.Name()
	}
	return l
}

// existing reports what occupies rel at the destination: "" (nothing),
// file, dir, symlink, other, or a blocking ancestor problem.
func (a *allocator) existing(rel string) (kind, actual string) {
	parts := strings.Split(rel, "/")
	dir := ""
	for i, comp := range parts {
		k, ok := a.listing(dir)[fold(comp)]
		if !ok {
			return "", ""
		}
		kind, name, _ := strings.Cut(k, "\x00")
		cur := path.Join(dir, name)
		if i == len(parts)-1 {
			return kind, cur
		}
		switch kind {
		case TypeDir:
		case TypeSymlink:
			return ConflictUnsafe, cur
		default:
			return ConflictAncestor, cur
		}
		dir = cur
	}
	return "", ""
}

func ancestors(rel string) []string {
	var out []string
	for d := path.Dir(rel); d != "." && d != "/"; d = path.Dir(d) {
		out = append(out, d)
	}
	return out
}

func (a *allocator) claimedAncestorFile(rel string) bool {
	for _, d := range ancestors(rel) {
		if _, ok := a.files[fold(d)]; ok {
			return true
		}
	}
	return false
}

func (a *allocator) free(rel string) bool {
	f := fold(rel)
	if _, ok := a.files[f]; ok || a.dirs[f] || a.claimedAncestorFile(rel) {
		return false
	}
	k, _ := a.existing(rel)
	return k == ""
}

func (a *allocator) claimFile(rel string) {
	a.files[fold(rel)] = rel
	for _, d := range ancestors(rel) {
		a.dirs[fold(d)] = true
	}
}

func (a *allocator) claimDir(rel string) {
	a.dirs[fold(rel)] = true
	for _, d := range ancestors(rel) {
		a.dirs[fold(d)] = true
	}
}

// suffixed renders "name (n).ext" — counting from 2, the original being
// conceptually copy 1 (spec §6.4).
func suffixed(rel string, n int, isDir bool) string {
	dir, base := path.Split(rel)
	ext := ""
	if !isDir {
		ext = path.Ext(base)
	}
	return dir + fmt.Sprintf("%s (%d)%s", strings.TrimSuffix(base, ext), n, ext)
}

func (a *allocator) allocate(rel string, isDir bool) (string, error) {
	for n := 2; n < 100000; n++ {
		if c := suffixed(rel, n, isDir); a.free(c) {
			return c, nil
		}
	}
	return "", fmt.Errorf("no free keep-both name for %s", rel)
}

func (a *allocator) block(it *item, kind, msg string) {
	it.entry.Action, it.entry.ConflictKind = model.ActionNeedsReview, kind
	a.findings = append(a.findings, model.Finding{Severity: model.SeverityBlocking, Kind: kind, PlanDestID: a.destID,
		SourceRel: it.inv.Label + ":" + it.scan.Rel, DestRel: it.entry.DestRelPath, Message: msg})
}

// placeGroup resolves a keep-together group at its root, never member by
// member: a collision moves the whole group to "Root (n)" with every
// internal path unchanged, or (under any other policy) blocks it whole.
func (a *allocator) placeGroup(members []*item) {
	base := members[0].r.GroupDest
	collision := ""
	k, actual := a.existing(base)
	switch {
	case k == ConflictUnsafe || k == ConflictAncestor || k == TypeSymlink || k == TypeOther:
		for _, m := range members {
			if m.entry.Action != model.ActionExclude {
				a.block(m, ConflictUnsafe, fmt.Sprintf("keep-together group destination %s crosses %s, which is not a plain directory", base, actual))
			}
		}
		return
	case k != "":
		collision = "the destination already has " + actual
	case a.dirs[fold(base)] || a.files[fold(base)] != "" || a.claimedAncestorFile(base):
		collision = "another item in this plan already uses " + base
	}
	target := base
	if collision != "" {
		if a.policy != preset.KeepBoth {
			for _, m := range members {
				if m.entry.Action != model.ActionExclude {
					a.block(m, ConflictGroupRoot, fmt.Sprintf("keep-together group %s collides (%s); the whole group needs review", base, collision))
				}
			}
			return
		}
		var err error
		if target, err = a.allocate(base, true); err != nil {
			for _, m := range members {
				a.block(m, ConflictGroupRoot, err.Error())
			}
			return
		}
		a.findings = append(a.findings, model.Finding{Severity: model.SeverityWarning, Kind: ConflictGroupRoot, PlanDestID: a.destID,
			SourceRel: members[0].inv.Label + ":" + members[0].r.GroupRoot, DestRel: target,
			Message: fmt.Sprintf("keep-together group moved from %s to %s because %s; internal paths unchanged", base, target, collision)})
	}
	a.claimDir(target)
	for _, m := range members {
		if m.entry.Action == model.ActionExclude || m.entry.Action == model.ActionNeedsReview {
			continue
		}
		if target != base {
			m.entry.RenamedFrom = m.entry.DestRelPath
			m.entry.DestRelPath = target + strings.TrimPrefix(m.entry.DestRelPath, base)
			m.entry.ConflictKind = ConflictGroupRoot
		}
		if m.scan.Type == TypeDir {
			a.claimDir(m.entry.DestRelPath)
		} else {
			a.claimFile(m.entry.DestRelPath)
		}
	}
}

func classifyPlanned(owner, rel string) string {
	switch {
	case owner == rel:
		return ConflictDuplicateTarget
	case norm.NFC.String(owner) == norm.NFC.String(rel):
		return ConflictUnicode
	default:
		return ConflictCaseOnly
	}
}

// placeFile places one ordinary copy entry.
func (a *allocator) placeFile(it *item) {
	d := it.entry.DestRelPath
	f := fold(d)
	kind := ""
	var existingActual string
	if owner, ok := a.files[f]; ok {
		kind = classifyPlanned(owner, d)
	} else if a.dirs[f] {
		kind = ConflictFileVsDir
	} else if a.claimedAncestorFile(d) {
		a.block(it, ConflictAncestor, "a planned file occupies a parent of "+d)
		return
	} else {
		k, actual := a.existing(d)
		existingActual = actual
		switch k {
		case "":
		case ConflictUnsafe:
			a.block(it, ConflictUnsafe, fmt.Sprintf("%s is a symlink; Ferry never writes through links at the destination", actual))
			return
		case ConflictAncestor:
			a.block(it, ConflictAncestor, fmt.Sprintf("%s exists as a file where %s needs a folder", actual, d))
			return
		case TypeSymlink, TypeOther:
			a.block(it, ConflictUnsafe, fmt.Sprintf("%s exists as a %s", actual, k))
			return
		case TypeDir:
			kind = ConflictFileVsDir
		default:
			kind = ConflictExistingFile
		}
	}
	if kind == "" {
		a.claimFile(d)
		return
	}
	if kind == ConflictExistingFile && a.policy == preset.SkipIdentical && existingActual == d && !a.remote {
		same, sum, err := a.identical(it, d)
		if err != nil {
			a.block(it, ConflictExistingFile, "could not compare with the existing file: "+err.Error())
			return
		}
		if same {
			it.entry.Action, it.entry.ConflictKind, it.entry.ExpectedHash = model.ActionSkipIdentical, ConflictExistingIdentical, sum
			a.claimFile(d)
			return
		}
		kind = ConflictDifferentContent
	}
	if a.policy == preset.NeedsReview {
		a.block(it, kind, fmt.Sprintf("%s: %s needs a decision", kind, d))
		return
	}
	alt, err := a.allocate(d, false)
	if err != nil {
		a.block(it, kind, err.Error())
		return
	}
	it.entry.RenamedFrom, it.entry.DestRelPath, it.entry.ConflictKind = d, alt, kind
	a.claimFile(alt)
}

// identical compares full content — never size or mtime alone.
func (a *allocator) identical(it *item, destRel string) (bool, string, error) {
	src, err := os.Open(filepath.Join(it.inv.Root, filepath.FromSlash(it.scan.Rel)))
	if err != nil {
		return false, "", err
	}
	defer func() { _ = src.Close() }()
	ss, _, err := checksum.Reader(a.algo, ctxReader{a.ctx, src})
	if err != nil {
		return false, "", err
	}
	dir, leaf, err := storage.OpenFileIn(a.root, destRel, false)
	if err != nil {
		return false, "", err
	}
	defer func() { _ = dir.Close() }()
	rb, _, err := dir.OpenReadback(leaf)
	if err != nil {
		return false, "", err
	}
	defer func() { _ = rb.Close() }()
	ds, _, err := checksum.Reader(a.algo, ctxReader{a.ctx, rb})
	if err != nil {
		return false, "", err
	}
	return ss == ds, ss, nil
}

// placeDir places a retained (empty) directory.
func (a *allocator) placeDir(it *item) {
	d := it.entry.DestRelPath
	if _, ok := a.files[fold(d)]; ok || a.claimedAncestorFile(d) {
		a.block(it, ConflictFileVsDir, "a planned file occupies "+d)
		return
	}
	switch k, actual := a.existing(d); k {
	case "", TypeDir:
	case ConflictUnsafe, TypeSymlink:
		a.block(it, ConflictUnsafe, actual+" is a symlink")
		return
	default:
		a.block(it, ConflictFileVsDir, actual+" exists and is not a folder")
		return
	}
	a.claimDir(d)
}

type ctxReader struct {
	ctx context.Context
	r   io.Reader
}

func (c ctxReader) Read(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	return c.r.Read(p)
}

var errSourceOverlap = errors.New("sources and destinations overlap")
