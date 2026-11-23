package metadata

import (
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// CardHint is a guess, with evidence, about what produced a source tree.
type CardHint struct {
	Vendor     string   `json:"vendor"`
	Format     string   `json:"format"`
	Confidence string   `json:"confidence"` // high | medium
	Evidence   []string `json:"evidence"`
}

type cardRule struct {
	vendor, format string
	match          func(rel string) bool
}

func hasPrefixFold(rel, prefix string) bool {
	return len(rel) >= len(prefix) && strings.EqualFold(rel[:len(prefix)], prefix)
}

func extIs(rel string, exts ...string) bool {
	e := strings.ToLower(path.Ext(rel))
	for _, x := range exts {
		if e == x {
			return true
		}
	}
	return false
}

var (
	reDJI     = regexp.MustCompile(`(?i)^DCIM/[^/]*MEDIA/DJI_[^/]+$`)
	reGoPro   = regexp.MustCompile(`(?i)^DCIM/\d{3}GOPRO/G[HXL]\d{6}\.(MP4|THM|LRV)$`)
	reFuji    = regexp.MustCompile(`(?i)^DCIM/\d{3}_FUJI/`)
	reCanonCC = regexp.MustCompile(`(?i)^DCIM/\d{3}CANON/`)
	reARRI    = regexp.MustCompile(`(?i)^[A-Z]\d{3}C\d{3}_\d{6}_[A-Z0-9]{4}`)
)

var cardRules = []cardRule{
	{"Sony", "XAVC (M4ROOT)", func(r string) bool { return hasPrefixFold(r, "PRIVATE/M4ROOT/") }},
	{"Sony", "XDCAM (XDROOT)", func(r string) bool { return hasPrefixFold(r, "XDROOT/") }},
	{"Canon", "Cinema RAW / XF-AVC", func(r string) bool { return hasPrefixFold(r, "CONTENTS/CLIPS") }},
	{"Canon", "DCIM", func(r string) bool { return reCanonCC.MatchString(r) || extIs(r, ".crm", ".cr3") }},
	{"RED", "R3D", func(r string) bool { return extIs(r, ".r3d") || strings.Contains(strings.ToUpper(r), ".RDC/") }},
	{"ARRI", "ARRIRAW / ProRes", func(r string) bool {
		return extIs(r, ".ari", ".arx") || (extIs(r, ".mxf", ".mov") && reARRI.MatchString(path.Base(r)))
	}},
	{"Blackmagic", "BRAW", func(r string) bool { return extIs(r, ".braw") }},
	{"Panasonic", "AVCHD / MOV", func(r string) bool {
		return hasPrefixFold(r, "PRIVATE/AVCHD/") || hasPrefixFold(r, "PRIVATE/PANA_GRP/")
	}},
	{"Fujifilm", "DCIM", func(r string) bool { return reFuji.MatchString(r) }},
	{"DJI", "DCIM", func(r string) bool { return reDJI.MatchString(r) }},
	{"GoPro", "DCIM", func(r string) bool { return reGoPro.MatchString(r) }},
}

// DetectCard fingerprints a source tree from its relative paths (and
// optionally probed media) without reading file bodies.
func DetectCard(rels []string, media map[string]Media) *CardHint {
	counts := map[[2]string]int{}
	evidence := map[[2]string][]string{}
	for _, rel := range rels {
		for _, cr := range cardRules {
			if cr.match(rel) {
				k := [2]string{cr.vendor, cr.format}
				counts[k]++
				if len(evidence[k]) < 3 {
					evidence[k] = append(evidence[k], rel)
				}
				break
			}
		}
	}
	// Audio recorders announce themselves in BWF bext.
	for rel, m := range media {
		if m.Source == "bwf" && m.Make != "" {
			k := [2]string{recorderVendor(m.Make), "BWF"}
			counts[k]++
			if len(evidence[k]) < 3 {
				evidence[k] = append(evidence[k], rel)
			}
		}
	}
	var best [2]string
	n := 0
	keys := make([][2]string, 0, len(counts))
	for k := range counts {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i][0]+keys[i][1] < keys[j][0]+keys[j][1] })
	for _, k := range keys {
		if counts[k] > n {
			best, n = k, counts[k]
		}
	}
	if n == 0 {
		return nil
	}
	conf := "medium"
	if n >= 2 {
		conf = "high"
	}
	return &CardHint{Vendor: best[0], Format: best[1], Confidence: conf, Evidence: evidence[best]}
}

func recorderVendor(originator string) string {
	u := strings.ToUpper(originator)
	switch {
	case strings.Contains(u, "ZOOM"):
		return "Zoom"
	case strings.Contains(u, "SOUND DEVICES"), strings.HasPrefix(u, "SD "), strings.Contains(u, "MIXPRE"):
		return "Sound Devices"
	case strings.Contains(u, "TASCAM"):
		return "Tascam"
	}
	return originator
}

var (
	reSonySidecar = regexp.MustCompile(`(?i)^(.+)M01\.XML$`)
	reGoProSide   = regexp.MustCompile(`(?i)^(G[HXL]\d{6})\.(THM|LRV)$`)
	reDJISide     = regexp.MustCompile(`(?i)^(DJI_.+)\.(SRT|LRF)$`)
)

// Companions pairs known vendor sidecars with their clip: Sony
// C0001M01.XML with C0001.MP4, GoPro .THM/.LRV, DJI .SRT/.LRF. Pairing is
// by documented vendor naming only — never a guessed basename join — and
// only within one directory. Returns sidecar rel -> clip rel.
func Companions(rels []string) map[string]string {
	byDir := map[string]map[string]string{} // dir -> lower(name) -> rel
	for _, r := range rels {
		d := path.Dir(r)
		if byDir[d] == nil {
			byDir[d] = map[string]string{}
		}
		byDir[d][strings.ToLower(path.Base(r))] = r
	}
	out := map[string]string{}
	for _, r := range rels {
		d, name := path.Dir(r), path.Base(r)
		var stems []string
		if m := reSonySidecar.FindStringSubmatch(name); m != nil {
			stems = []string{m[1] + ".mp4", m[1] + ".mxf"}
		} else if m := reGoProSide.FindStringSubmatch(name); m != nil {
			stems = []string{m[1] + ".mp4"}
		} else if m := reDJISide.FindStringSubmatch(name); m != nil {
			stems = []string{m[1] + ".mp4", m[1] + ".mov"}
		}
		for _, s := range stems {
			if clip, ok := byDir[d][strings.ToLower(s)]; ok {
				out[r] = clip
				break
			}
		}
	}
	return out
}

// Probe reads metadata for every rel under root, merges each Sony XML
// sidecar into its clip, and gives every companion its clip's metadata so
// the two route together. Unreadable files are reported, not fatal.
func Probe(root string, rels []string) (map[string]Media, map[string]error) {
	media := map[string]Media{}
	errs := map[string]error{}
	for _, r := range rels {
		if _, ok := readers[strings.ToLower(path.Ext(r))]; !ok {
			continue
		}
		m, err := ProbeFile(filepath.Join(root, filepath.FromSlash(r)))
		if err != nil {
			errs[r] = err
			continue
		}
		if !m.Empty() {
			media[r] = m
		}
	}
	comp := Companions(rels)
	// Sidecar evidence (device serial, recorded date) enriches the clip.
	for side, clip := range comp {
		if sm, ok := media[side]; ok && sm.Source == "sony_xml" {
			media[clip] = sm.Merge(media[clip])
		}
	}
	for side, clip := range comp {
		cm := media[clip]
		cm.CompanionOf = clip
		media[side] = cm
	}
	return media, errs
}
