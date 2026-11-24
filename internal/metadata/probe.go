// Package metadata reads camera and capture metadata from container
// headers in pure Go: no FFmpeg, no frame decoding. It reads box, IFD and
// chunk headers by seeking, so a 100 GB clip costs a few kilobytes of I/O.
//
// Absence is honest: a file with no embedded capture time has an empty
// CaptureDate, never "today" and never its modification time.
package metadata

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"strings"
	"time"
)

// Media is what Ferry knows about one file's capture.
type Media struct {
	CaptureTime string `json:"capture_time,omitempty"` // RFC 3339 with the offset the device recorded
	CaptureDate string `json:"capture_date,omitempty"` // YYYY-MM-DD in that offset
	Make        string `json:"make,omitempty"`
	Model       string `json:"model,omitempty"`
	Serial      string `json:"serial,omitempty"`
	Reel        string `json:"reel,omitempty"`
	Scene       string `json:"scene,omitempty"`
	Take        string `json:"take,omitempty"`
	Width       int    `json:"width,omitempty"`
	Height      int    `json:"height,omitempty"`
	Source      string `json:"source,omitempty"` // which reader produced it
	CompanionOf string `json:"companion_of,omitempty"`
}

// Empty reports no evidence at all.
func (m Media) Empty() bool { return m == Media{} }

// JSON renders m compactly ("" when empty).
func (m Media) JSON() string {
	if m.Empty() {
		return ""
	}
	b, _ := json.Marshal(m)
	return string(b)
}

// CameraKey identifies one physical camera for role bindings.
func (m Media) CameraKey() string {
	if m.Serial == "" {
		return ""
	}
	return CameraKey(m.Make, m.Model, m.Serial)
}

// CameraKey normalizes (make, model, serial).
func CameraKey(make, model, serial string) string {
	return strings.ToLower(strings.TrimSpace(make)) + "|" + strings.ToLower(strings.TrimSpace(model)) + "|" + strings.TrimSpace(serial)
}

// ResolutionBucket names a frame size class.
func (m Media) ResolutionBucket() string {
	w := m.Width
	if m.Height > w {
		w = m.Height // portrait
	}
	switch {
	case w >= 7680:
		return "8K"
	case w >= 5760:
		return "6K"
	case w >= 3840:
		return "4K"
	case w >= 2560:
		return "2.5K"
	case w >= 1920:
		return "1080p"
	case w >= 1280:
		return "720p"
	case w > 0:
		return "SD"
	}
	return ""
}

func (m *Media) setTime(t time.Time) {
	if t.IsZero() || t.Year() < 1971 || t.Year() > 2200 {
		return
	}
	m.CaptureTime = t.Format(time.RFC3339)
	m.CaptureDate = t.Format("2006-01-02")
}

// Merge fills empty fields of m from o.
func (m Media) Merge(o Media) Media {
	fill := func(dst *string, src string) {
		if *dst == "" {
			*dst = src
		}
	}
	fill(&m.CaptureTime, o.CaptureTime)
	fill(&m.CaptureDate, o.CaptureDate)
	fill(&m.Make, o.Make)
	fill(&m.Model, o.Model)
	fill(&m.Serial, o.Serial)
	fill(&m.Reel, o.Reel)
	fill(&m.Scene, o.Scene)
	fill(&m.Take, o.Take)
	fill(&m.Source, o.Source)
	if m.Width == 0 && m.Height == 0 {
		m.Width, m.Height = o.Width, o.Height
	}
	return m
}

// maxSidecar bounds how much of an XML sidecar is read.
const maxSidecar = 1 << 20

// ProbeFile reads whatever metadata its extension promises. Unknown
// extensions return empty Media and no error.
func ProbeFile(p string) (Media, error) {
	ext := strings.ToLower(path.Ext(p))
	reader, ok := readers[ext]
	if !ok {
		return Media{}, nil
	}
	f, err := os.Open(p)
	if err != nil {
		return Media{}, err
	}
	defer func() { _ = f.Close() }()
	fi, err := f.Stat()
	if err != nil {
		return Media{}, err
	}
	m, err := reader(f, fi.Size())
	if err != nil {
		return Media{}, fmt.Errorf("%s: %w", p, err)
	}
	return m, nil
}

type readerFunc func(io.ReaderAt, int64) (Media, error)

var readers = map[string]readerFunc{
	".mp4": ReadMP4, ".mov": ReadMP4, ".m4v": ReadMP4, ".braw": ReadMP4, ".crm": ReadMP4, ".3gp": ReadMP4,
	".jpg": ReadEXIF, ".jpeg": ReadEXIF, ".tif": ReadEXIF, ".tiff": ReadEXIF, ".dng": ReadEXIF,
	".arw": ReadEXIF, ".cr2": ReadEXIF, ".nef": ReadEXIF, ".raf": readRAF,
	".wav": ReadWAV, ".bwf": ReadWAV,
	".xml": readSidecarXML,
}

func readSidecarXML(r io.ReaderAt, size int64) (Media, error) {
	n := min(size, maxSidecar)
	buf := make([]byte, n)
	if _, err := r.ReadAt(buf, 0); err != nil && err != io.EOF {
		return Media{}, err
	}
	return ParseSonyXML(buf), nil
}

// RAF (Fujifilm) embeds a JPEG whose EXIF carries the camera fields.
func readRAF(r io.ReaderAt, size int64) (Media, error) {
	hdr := make([]byte, 92)
	if _, err := r.ReadAt(hdr, 0); err != nil {
		return Media{}, nil
	}
	if string(hdr[:15]) != "FUJIFILMCCD-RAW" {
		return Media{}, nil
	}
	off := int64(be32(hdr[84:88]))
	ln := int64(be32(hdr[88:92]))
	if off <= 0 || ln <= 0 || off+ln > size {
		return Media{}, nil
	}
	return ReadEXIF(io.NewSectionReader(r, off, ln), ln)
}
