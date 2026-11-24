package identity

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

// Resolution states.
const (
	Available         = "available"
	Offline           = "offline"
	NeedsConfirmation = "needs_confirmation"
	Ambiguous         = "ambiguous"
	Unwritable        = "unwritable"
)

// Saved is the identity half of a saved destination.
type Saved struct {
	Identity    VolumeIdentity
	SubPath     string // folder inside the mount, relative ("" = mount root)
	LastBinding string // absolute path it was last bound to
}

// Observation is one mounted volume as seen now.
type Observation struct {
	MountPath string
	Identity  *VolumeIdentity
	Writable  bool
}

// Resolution is the resolver's verdict.
type Resolution struct {
	State      string   `json:"state"`
	Reason     string   `json:"reason"`
	Binding    string   `json:"binding,omitempty"`
	Candidates []string `json:"candidates,omitempty"`
}

// Resolve decides whether a saved destination is usable now. Each branch
// exists for a failure that has cost someone data:
//
//  1. Exactly one strong identity match: resolve the subfolder within it.
//  2. More than one strong match: ambiguous, never a silent tie-break.
//  3. Weak/medium match only: needs_confirmation.
//  4. Same share name on another host: needs_confirmation (alias or not?).
//  5. Nothing matched but the old path exists: needs_confirmation — a share
//     unmounted and left its mount directory on the local disk.
//  6. Nothing matched and nothing there: offline.
//
// A saved identity that is not strong itself can never resolve available.
func Resolve(saved Saved, obs []Observation) Resolution {
	var strong, weak, alias []Observation
	for _, o := range obs {
		switch classify(saved.Identity, o.Identity) {
		case "strong":
			strong = append(strong, o)
		case "weak":
			weak = append(weak, o)
		case "alias":
			alias = append(alias, o)
		}
	}
	if len(strong) > 1 {
		return Resolution{State: Ambiguous, Candidates: candidates(saved, strong),
			Reason: fmt.Sprintf("%d connected volumes report the same identity (%q); choose one explicitly", len(strong), saved.Identity.Value)}
	}
	if len(strong) == 1 {
		return within(saved, strong[0])
	}
	if len(weak) > 0 {
		return Resolution{State: NeedsConfirmation, Candidates: candidates(saved, weak),
			Reason: "a connected volume carries this destination's identifier, but the evidence is not strong enough to rebind automatically; confirm it"}
	}
	if len(alias) > 0 {
		return Resolution{State: NeedsConfirmation, Candidates: candidates(saved, alias),
			Reason: fmt.Sprintf("a share named %q is mounted from a different host than the saved %q; confirm before transferring",
				ShareName(saved.Identity.Value), ShareHost(saved.Identity.Value))}
	}
	return noMatch(saved, obs)
}

func classify(saved VolumeIdentity, got *VolumeIdentity) string {
	if got == nil || got.Kind == KindPathOnly || saved.Kind == KindPathOnly {
		return ""
	}
	if got.Kind == saved.Kind && got.Value == saved.Value {
		if saved.Confidence == Strong && got.Confidence == Strong {
			return "strong"
		}
		return "weak"
	}
	if saved.Kind == KindServerShare && got.Kind == KindServerShare &&
		ShareName(saved.Value) != "" && ShareName(saved.Value) == ShareName(got.Value) &&
		ShareHost(saved.Value) != ShareHost(got.Value) {
		return "alias"
	}
	return ""
}

func candidates(saved Saved, obs []Observation) []string {
	out := make([]string, 0, len(obs))
	for _, o := range obs {
		out = append(out, candidatePath(saved, o))
	}
	return out
}

func candidatePath(saved Saved, o Observation) string {
	if saved.SubPath == "" {
		return o.MountPath
	}
	return filepath.Join(o.MountPath, filepath.FromSlash(saved.SubPath))
}

func within(saved Saved, o Observation) Resolution {
	cand := candidatePath(saved, o)
	if !o.Writable {
		return Resolution{State: Unwritable, Binding: cand, Candidates: []string{cand},
			Reason: "the matched storage is mounted read-only at " + o.MountPath}
	}
	fi, err := os.Stat(cand)
	if err != nil {
		return Resolution{State: NeedsConfirmation, Candidates: []string{o.MountPath},
			Reason: fmt.Sprintf("the storage was recognized at %s, but the saved folder %q is not there; Ferry does not create a destination folder on its own", o.MountPath, saved.SubPath)}
	}
	if !fi.IsDir() {
		return Resolution{State: Unwritable, Candidates: []string{cand}, Reason: cand + " exists but is not a directory"}
	}
	if esc := escapes(cand, o.MountPath); esc != "" {
		return Resolution{State: NeedsConfirmation, Candidates: []string{cand}, Reason: esc}
	}
	if unix.Access(cand, unix.W_OK|unix.X_OK) != nil {
		return Resolution{State: Unwritable, Binding: cand, Candidates: []string{cand}, Reason: cand + " is not writable by this user"}
	}
	return Resolution{State: Available, Binding: cand, Candidates: []string{cand},
		Reason: fmt.Sprintf("recognized by %s at %s", saved.Identity.Kind, o.MountPath)}
}

// escapes reports why cand does not live on the mount we recognized (a
// symlinked subfolder pointing elsewhere), or "".
func escapes(cand, mount string) string {
	rc, err1 := filepath.EvalSymlinks(cand)
	rm, err2 := filepath.EvalSymlinks(mount)
	if err1 != nil || err2 != nil {
		return "could not resolve " + cand
	}
	if rc != rm && !strings.HasPrefix(rc, rm+string(filepath.Separator)) {
		return fmt.Sprintf("%s resolves to %s, outside the recognized storage at %s", cand, rc, mount)
	}
	var a, b unix.Stat_t
	if unix.Stat(rc, &a) == nil && unix.Stat(rm, &b) == nil && a.Dev != b.Dev {
		return fmt.Sprintf("%s is on a different device than the recognized storage at %s", cand, mount)
	}
	return ""
}

func noMatch(saved Saved, obs []Observation) Resolution {
	for _, o := range obs {
		if o.Identity != nil && o.Identity.Kind != KindPathOnly && saved.LastBinding != "" &&
			(o.MountPath == saved.LastBinding || strings.HasPrefix(saved.LastBinding, o.MountPath+"/")) && o.MountPath != "/" {
			return Resolution{State: NeedsConfirmation, Candidates: []string{saved.LastBinding},
				Reason: fmt.Sprintf("storage is mounted at %s but reports %s %q, not this destination's identity; a different disk or share is in that place",
					o.MountPath, o.Identity.Kind, o.Identity.Value)}
		}
	}
	if saved.LastBinding != "" {
		if fi, err := os.Stat(saved.LastBinding); err == nil && fi.IsDir() {
			return Resolution{State: NeedsConfirmation, Candidates: []string{saved.LastBinding},
				Reason: saved.LastBinding + " still exists on disk, but nothing mounted there matches this destination's identity — " +
					"after a share unmounts its mount directory is left on the local disk. Ferry will not write into it"}
		}
	}
	return Resolution{State: Offline, Reason: "no connected storage matches this destination's saved identity"}
}
