package metadata_test

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Lunar-Park/lp-ferry/internal/metadata"
	mt "github.com/Lunar-Park/lp-ferry/internal/metadata/metadatatest"
)

func write(t *testing.T, root, rel string, b []byte) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestMP4HeadersOnly(t *testing.T) {
	for _, last := range []bool{false, true} {
		b := mt.MP4{Created: time.Date(2026, 11, 22, 1, 2, 3, 0, time.UTC), Make: "Sony", Model: "ILME-FX3",
			Width: 3840, Height: 2160, MdatBytes: 1 << 20, MoovLast: last}.Bytes()
		m, err := metadata.ReadMP4(bytes.NewReader(b), int64(len(b)))
		if err != nil {
			t.Fatal(err)
		}
		if m.Make != "Sony" || m.Model != "ILME-FX3" || m.CaptureDate != "2026-11-22" || m.ResolutionBucket() != "4K" {
			t.Fatalf("moovLast=%v: %+v", last, m)
		}
	}
	// ©day with an offset wins over mvhd and keeps the device's local date.
	b := mt.MP4{Created: time.Date(2026, 11, 21, 23, 0, 0, 0, time.UTC), Day: "2026-11-22T08:00:00+0900", Width: 1920, Height: 1080}.Bytes()
	m, _ := metadata.ReadMP4(bytes.NewReader(b), int64(len(b)))
	if m.CaptureDate != "2026-11-22" || m.CaptureTime != "2026-11-22T08:00:00+09:00" {
		t.Fatalf("%+v", m)
	}
	// No creation time anywhere: no date, never a guess.
	b = mt.MP4{Width: 1920, Height: 1080}.Bytes()
	m, _ = metadata.ReadMP4(bytes.NewReader(b), int64(len(b)))
	if m.CaptureDate != "" {
		t.Fatalf("fabricated a date: %+v", m)
	}
	// Garbage is not an error, just no metadata.
	if m, err := metadata.ReadMP4(bytes.NewReader([]byte("not a movie at all")), 18); err != nil || !m.Empty() {
		t.Fatalf("%+v %v", m, err)
	}
}

func TestEXIFAndBWFAndSonyXML(t *testing.T) {
	j := mt.JPEG("FUJIFILM", "X-T5", "2026:11:22 14:30:00", "-08:00", "SN42")
	m, err := metadata.ReadEXIF(bytes.NewReader(j), int64(len(j)))
	if err != nil || m.Make != "FUJIFILM" || m.Model != "X-T5" || m.Serial != "SN42" || m.CaptureTime != "2026-11-22T14:30:00-08:00" {
		t.Fatalf("%+v %v", m, err)
	}
	w := mt.WAV("ZOOM F6", "2026-11-22", "09:15:00", "<BWFXML><PROJECT>Doc</PROJECT><SCENE>12A</SCENE><TAKE>3</TAKE><TAPE>R07</TAPE></BWFXML>", 100)
	m, err = metadata.ReadWAV(bytes.NewReader(w), int64(len(w)))
	if err != nil || m.Make != "ZOOM F6" || m.CaptureDate != "2026-11-22" || m.Scene != "12A" || m.Reel != "R07" {
		t.Fatalf("%+v %v", m, err)
	}
	x := metadata.ParseSonyXML(mt.SonyXML("Sony", "ILME-FX3", "12345", "2026-11-22T10:00:00+09:00", 3840, 2160))
	if x.Serial != "12345" || x.CaptureDate != "2026-11-22" || x.Width != 3840 || x.CameraKey() != "sony|ilme-fx3|12345" {
		t.Fatalf("%+v", x)
	}
}

func TestCardDetectionAndCompanions(t *testing.T) {
	root := t.TempDir()
	rels := []string{
		"PRIVATE/M4ROOT/CLIP/C0001.MP4", "PRIVATE/M4ROOT/CLIP/C0001M01.XML",
		"PRIVATE/M4ROOT/CLIP/C0002.MP4", "PRIVATE/M4ROOT/MEDIAPRO.XML",
	}
	for i, r := range rels {
		switch filepath.Ext(r) {
		case ".MP4":
			write(t, root, r, mt.MP4{Width: 3840, Height: 2160, MdatBytes: 64 + i}.Bytes())
		default:
			write(t, root, r, mt.SonyXML("Sony", "ILME-FX3", "12345", "2026-11-22T10:00:00+09:00", 3840, 2160))
		}
	}
	hint := metadata.DetectCard(rels, nil)
	if hint == nil || hint.Vendor != "Sony" || hint.Confidence != "high" {
		t.Fatalf("%+v", hint)
	}
	comp := metadata.Companions(rels)
	if comp["PRIVATE/M4ROOT/CLIP/C0001M01.XML"] != "PRIVATE/M4ROOT/CLIP/C0001.MP4" || len(comp) != 1 {
		t.Fatalf("%v", comp)
	}
	media, errs := metadata.Probe(root, rels)
	if len(errs) != 0 {
		t.Fatal(errs)
	}
	clip := media["PRIVATE/M4ROOT/CLIP/C0001.MP4"]
	if clip.Serial != "12345" || clip.CaptureDate != "2026-11-22" {
		t.Fatalf("sidecar evidence not merged into clip: %+v", clip)
	}
	side := media["PRIVATE/M4ROOT/CLIP/C0001M01.XML"]
	if side.CompanionOf != "PRIVATE/M4ROOT/CLIP/C0001.MP4" || side.CaptureDate != "2026-11-22" {
		t.Fatalf("companion: %+v", side)
	}
	if media["PRIVATE/M4ROOT/CLIP/C0002.MP4"].CaptureDate != "" {
		t.Fatal("clip without evidence got a date")
	}
	if metadata.DetectCard([]string{"Docs/a.pdf"}, nil) != nil {
		t.Fatal("ordinary folder detected as a card")
	}
}

func TestProbeThousandClipsIsFast(t *testing.T) {
	root := t.TempDir()
	clip := mt.MP4{Created: time.Now(), Width: 1920, Height: 1080, MdatBytes: 256 << 10, MoovLast: true}.Bytes()
	var rels []string
	for i := 0; i < 1000; i++ {
		r := filepath.ToSlash(filepath.Join("CLIP", "C"+string(rune('A'+i%26))+time.Duration(i).String()+".MP4"))
		write(t, root, r, clip)
		rels = append(rels, r)
	}
	start := time.Now()
	media, errs := metadata.Probe(root, rels)
	el := time.Since(start)
	if len(errs) != 0 || len(media) != 1000 {
		t.Fatalf("%d %v", len(media), errs)
	}
	t.Logf("1000 clips probed in %s", el)
	if el > 2*time.Second { // generous for -race; target is <150ms uninstrumented
		t.Fatalf("probing 1000 clips took %s", el)
	}
}
