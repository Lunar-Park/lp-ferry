package metadata

import (
	"bytes"
	"encoding/binary"
	"encoding/xml"
	"errors"
	"io"
	"strconv"
	"strings"
	"time"
)

func be16(b []byte) uint16 { return binary.BigEndian.Uint16(b) }
func be32(b []byte) uint32 { return binary.BigEndian.Uint32(b) }
func be64(b []byte) uint64 { return binary.BigEndian.Uint64(b) }

// ---------------------------------------------------------------------
// ISO BMFF / QuickTime
// ---------------------------------------------------------------------

// maxBoxes bounds the walk so a malformed file cannot loop forever.
const maxBoxes = 4096

type box struct {
	typ        string
	start, end int64 // payload range
}

// boxes lists the child boxes in [start, end).
func boxes(r io.ReaderAt, start, end int64) ([]box, error) {
	var out []box
	hdr := make([]byte, 16)
	for pos := start; pos+8 <= end && len(out) < maxBoxes; {
		if _, err := r.ReadAt(hdr[:8], pos); err != nil {
			return out, err
		}
		size := int64(be32(hdr[:4]))
		typ := string(hdr[4:8])
		hlen := int64(8)
		switch size {
		case 1:
			if _, err := r.ReadAt(hdr[8:16], pos+8); err != nil {
				return out, err
			}
			size = int64(be64(hdr[8:16]))
			hlen = 16
		case 0:
			size = end - pos
		}
		if size < hlen || pos+size > end {
			return out, errors.New("malformed box " + strconv.Quote(typ))
		}
		out = append(out, box{typ: typ, start: pos + hlen, end: pos + size})
		pos += size
	}
	return out, nil
}

func find(bs []box, typ string) (box, bool) {
	for _, b := range bs {
		if b.typ == typ {
			return b, true
		}
	}
	return box{}, false
}

func readRange(r io.ReaderAt, b box, limit int64) []byte {
	n := min(b.end-b.start, limit)
	if n <= 0 {
		return nil
	}
	buf := make([]byte, n)
	if _, err := r.ReadAt(buf, b.start); err != nil && err != io.EOF {
		return nil
	}
	return buf
}

var macEpoch = time.Date(1904, 1, 1, 0, 0, 0, 0, time.UTC)

// ReadMP4 reads moov/mvhd, track dimensions and QuickTime udta / mdta
// metadata. It never reads mdat.
func ReadMP4(r io.ReaderAt, size int64) (Media, error) {
	top, err := boxes(r, 0, size)
	if err != nil && len(top) == 0 {
		return Media{}, nil // not an ISO BMFF file; not an error
	}
	moov, ok := find(top, "moov")
	if !ok {
		return Media{}, nil
	}
	kids, _ := boxes(r, moov.start, moov.end)
	var m Media
	m.Source = "mp4"
	// Device-recorded strings beat mvhd, which many cameras write in UTC
	// with no offset (and some write local time as if it were UTC).
	if udta, ok := find(kids, "udta"); ok {
		m = m.Merge(readUdta(r, udta))
	}
	if meta, ok := find(kids, "meta"); ok {
		m = m.Merge(readMdta(r, meta))
	}
	if m.CaptureTime == "" {
		if mvhd, ok := find(kids, "mvhd"); ok {
			b := readRange(r, mvhd, 32)
			var secs uint64
			if len(b) >= 8 && b[0] == 0 {
				secs = uint64(be32(b[4:8]))
			} else if len(b) >= 12 && b[0] == 1 {
				secs = be64(b[4:12])
			}
			if secs > 0 {
				m.setTime(macEpoch.Add(time.Duration(secs) * time.Second))
			}
		}
	}
	for _, k := range kids {
		if k.typ != "trak" || m.Width != 0 {
			continue
		}
		tk, _ := boxes(r, k.start, k.end)
		if tkhd, ok := find(tk, "tkhd"); ok {
			b := readRange(r, tkhd, 96)
			off := 76 // v0: width/height at 76/80 (16.16 fixed)
			if len(b) > 0 && b[0] == 1 {
				off = 88
			}
			if len(b) >= off+8 {
				w, h := int(be32(b[off:off+4])>>16), int(be32(b[off+4:off+8])>>16)
				if w > 0 && h > 0 {
					m.Width, m.Height = w, h
				}
			}
		}
	}
	return m, nil
}

// readUdta reads ©mak ©mod ©day and ©nam-style QuickTime user data atoms.
func readUdta(r io.ReaderAt, udta box) Media {
	var m Media
	kids, _ := boxes(r, udta.start, udta.end)
	for _, k := range kids {
		raw := readRange(r, k, 512)
		if len(raw) < 4 {
			continue
		}
		// Classic QuickTime text: 2-byte length, 2-byte language, text.
		ln := int(be16(raw[:2]))
		val := ""
		if ln > 0 && 4+ln <= len(raw) {
			val = strings.TrimSpace(string(raw[4 : 4+ln]))
		}
		switch k.typ {
		case "\xa9mak":
			m.Make = val
		case "\xa9mod":
			m.Model = val
		case "\xa9day":
			if t, ok := parseTime(val); ok {
				m.setTime(t)
			}
		}
	}
	return m
}

// readMdta reads meta/keys/ilst (com.apple.quicktime.* keys).
func readMdta(r io.ReaderAt, meta box) Media {
	var m Media
	kids, _ := boxes(r, meta.start, meta.end)
	if len(kids) == 0 {
		// ISO meta is a full box (4-byte version/flags) before children.
		kids, _ = boxes(r, meta.start+4, meta.end)
	}
	keysBox, ok1 := find(kids, "keys")
	ilst, ok2 := find(kids, "ilst")
	if !ok1 || !ok2 {
		return m
	}
	kb := readRange(r, keysBox, 64<<10)
	if len(kb) < 8 {
		return m
	}
	count := int(be32(kb[4:8]))
	var keys []string
	for pos := 8; pos+8 <= len(kb) && len(keys) < count; {
		sz := int(be32(kb[pos : pos+4]))
		if sz < 8 || pos+sz > len(kb) {
			break
		}
		keys = append(keys, string(kb[pos+8:pos+sz]))
		pos += sz
	}
	items, _ := boxes(r, ilst.start, ilst.end)
	for _, it := range items {
		idx := int(be32([]byte(it.typ))) - 1
		if idx < 0 || idx >= len(keys) {
			continue
		}
		data, _ := boxes(r, it.start, it.end)
		d, ok := find(data, "data")
		if !ok {
			continue
		}
		raw := readRange(r, d, 1024)
		if len(raw) < 8 {
			continue
		}
		val := strings.TrimSpace(string(raw[8:]))
		switch keys[idx] {
		case "com.apple.quicktime.make":
			m.Make = val
		case "com.apple.quicktime.model":
			m.Model = val
		case "com.apple.quicktime.creationdate":
			if t, ok := parseTime(val); ok {
				m.setTime(t)
			}
		}
	}
	return m
}

func parseTime(s string) (time.Time, bool) {
	s = strings.TrimSpace(s)
	for _, layout := range []string{time.RFC3339, "2006-01-02T15:04:05-0700", "2006-01-02T15:04:05Z0700", "2006-01-02T15:04:05",
		"2006:01:02 15:04:05", "2006-01-02 15:04:05", "2006-01-02"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// ---------------------------------------------------------------------
// EXIF (JPEG APP1 and TIFF-based raw)
// ---------------------------------------------------------------------

// ReadEXIF reads Make, Model, serial and DateTimeOriginal.
func ReadEXIF(r io.ReaderAt, size int64) (Media, error) {
	head := make([]byte, 4)
	if _, err := r.ReadAt(head, 0); err != nil {
		return Media{}, nil
	}
	var tiff io.ReaderAt
	var tlen int64
	switch {
	case head[0] == 0xFF && head[1] == 0xD8:
		off, ln, ok := findAPP1(r, size)
		if !ok {
			return Media{}, nil
		}
		tiff, tlen = io.NewSectionReader(r, off, ln), ln
	case (head[0] == 'I' && head[1] == 'I') || (head[0] == 'M' && head[1] == 'M'):
		tiff, tlen = r, size
	default:
		return Media{}, nil
	}
	return parseTIFF(tiff, tlen), nil
}

func findAPP1(r io.ReaderAt, size int64) (int64, int64, bool) {
	hdr := make([]byte, 10)
	for pos := int64(2); pos+4 <= size && pos < 1<<20; {
		if _, err := r.ReadAt(hdr[:4], pos); err != nil || hdr[0] != 0xFF {
			return 0, 0, false
		}
		marker := hdr[1]
		ln := int64(be16(hdr[2:4]))
		if marker == 0xDA || ln < 2 {
			return 0, 0, false
		}
		if marker == 0xE1 {
			if _, err := r.ReadAt(hdr[:10], pos+4); err == nil && string(hdr[:6]) == "Exif\x00\x00" {
				return pos + 10, ln - 8, true
			}
		}
		pos += 2 + ln
	}
	return 0, 0, false
}

type tiffReader struct {
	r     io.ReaderAt
	size  int64
	order binary.ByteOrder
}

func parseTIFF(r io.ReaderAt, size int64) Media {
	hdr := make([]byte, 8)
	if _, err := r.ReadAt(hdr, 0); err != nil {
		return Media{}
	}
	t := &tiffReader{r: r, size: size, order: binary.BigEndian}
	if hdr[0] == 'I' {
		t.order = binary.LittleEndian
	}
	m := Media{Source: "exif"}
	var dto, offset string
	ifd := int64(t.order.Uint32(hdr[4:8]))
	t.walk(ifd, func(tag uint16, val func() string, ptr func() int64) {
		switch tag {
		case 0x010F:
			m.Make = val()
		case 0x0110:
			m.Model = val()
		case 0x0132:
			if dto == "" {
				dto = val()
			}
		case 0x8769:
			t.walk(ptr(), func(tag uint16, val func() string, _ func() int64) {
				switch tag {
				case 0x9003:
					dto = val()
				case 0x9011:
					offset = val()
				case 0xA431:
					m.Serial = val()
				}
			})
		}
	})
	if dto != "" {
		if offset != "" {
			if t, err := time.Parse("2006:01:02 15:04:05-07:00", dto+offset); err == nil {
				m.setTime(t)
				return m
			}
		}
		// No offset recorded: the clock's local time, labeled as such.
		if t, err := time.Parse("2006:01:02 15:04:05", dto); err == nil {
			m.CaptureTime = t.Format("2006-01-02T15:04:05")
			m.CaptureDate = t.Format("2006-01-02")
		}
	}
	return m
}

func (t *tiffReader) walk(off int64, fn func(tag uint16, val func() string, ptr func() int64)) {
	if off <= 0 || off+2 > t.size {
		return
	}
	cnt := make([]byte, 2)
	if _, err := t.r.ReadAt(cnt, off); err != nil {
		return
	}
	n := int(t.order.Uint16(cnt))
	if n > 512 {
		return
	}
	ent := make([]byte, 12)
	for i := 0; i < n; i++ {
		if _, err := t.r.ReadAt(ent, off+2+int64(i)*12); err != nil {
			return
		}
		tag := t.order.Uint16(ent[0:2])
		typ := t.order.Uint16(ent[2:4])
		count := int64(t.order.Uint32(ent[4:8]))
		raw := append([]byte(nil), ent[8:12]...)
		val := func() string {
			if typ != 2 || count <= 0 || count > 256 {
				return ""
			}
			b := raw[:min(count, 4)]
			if count > 4 {
				b = make([]byte, count)
				if _, err := t.r.ReadAt(b, int64(t.order.Uint32(raw))); err != nil {
					return ""
				}
			}
			return strings.TrimSpace(strings.TrimRight(string(b), "\x00"))
		}
		ptr := func() int64 { return int64(t.order.Uint32(raw)) }
		fn(tag, val, ptr)
	}
}

// ---------------------------------------------------------------------
// WAV / Broadcast Wave (bext + iXML)
// ---------------------------------------------------------------------

// ReadWAV reads the bext origination stamp and recorder, plus iXML
// project/scene/take/tape.
func ReadWAV(r io.ReaderAt, size int64) (Media, error) {
	hdr := make([]byte, 12)
	if _, err := r.ReadAt(hdr, 0); err != nil || string(hdr[0:4]) != "RIFF" || string(hdr[8:12]) != "WAVE" {
		return Media{}, nil
	}
	m := Media{Source: "bwf"}
	ch := make([]byte, 8)
	for pos, i := int64(12), 0; pos+8 <= size && i < 256; i++ {
		if _, err := r.ReadAt(ch, pos); err != nil {
			break
		}
		id := string(ch[:4])
		ln := int64(binary.LittleEndian.Uint32(ch[4:8]))
		body := pos + 8
		switch id {
		case "bext":
			b := make([]byte, min(ln, 400))
			if _, err := r.ReadAt(b, body); err == nil && len(b) >= 338 {
				m.Make = cstr(b[256:288]) // Originator, e.g. "ZOOM F6"
				date, clock := cstr(b[320:330]), cstr(b[330:338])
				date = strings.NewReplacer(":", "-", "/", "-", ".", "-").Replace(date)
				clock = strings.NewReplacer("-", ":", ".", ":").Replace(clock)
				if t, err := time.Parse("2006-01-02T15:04:05", date+"T"+clock); err == nil {
					// BWF stamps have no offset: the recorder's local clock.
					m.CaptureTime = t.Format("2006-01-02T15:04:05")
					m.CaptureDate = t.Format("2006-01-02")
				}
			}
		case "iXML":
			b := make([]byte, min(ln, maxSidecar))
			if _, err := r.ReadAt(b, body); err == nil {
				var ix struct {
					Project string `xml:"PROJECT"`
					Scene   string `xml:"SCENE"`
					Take    string `xml:"TAKE"`
					Tape    string `xml:"TAPE"`
				}
				if xml.Unmarshal(bytes.TrimRight(b, "\x00"), &ix) == nil {
					m.Scene, m.Take, m.Reel = ix.Scene, ix.Take, ix.Tape
				}
			}
		}
		pos = body + ln + ln%2
	}
	if m.Make != "" {
		m.Model = m.Make
	}
	return m, nil
}

func cstr(b []byte) string {
	if i := bytes.IndexByte(b, 0); i >= 0 {
		b = b[:i]
	}
	return strings.TrimSpace(string(b))
}

// ---------------------------------------------------------------------
// Sony NonRealTimeMeta XML sidecars (C0001M01.XML)
// ---------------------------------------------------------------------

// ParseSonyXML reads device and creation date from a Sony clip sidecar.
// Non-Sony XML returns empty Media.
func ParseSonyXML(b []byte) Media {
	var doc struct {
		XMLName      xml.Name
		CreationDate struct {
			Value string `xml:"value,attr"`
		} `xml:"CreationDate"`
		Device struct {
			Manufacturer string `xml:"manufacturer,attr"`
			Model        string `xml:"modelName,attr"`
			Serial       string `xml:"serialNo,attr"`
		} `xml:"Device"`
		VideoFormat struct {
			Layout struct {
				Pixel string `xml:"pixel,attr"`
				Lines string `xml:"numOfVerticalLine,attr"`
			} `xml:"VideoLayout"`
		} `xml:"VideoFormat"`
	}
	if xml.Unmarshal(b, &doc) != nil || doc.XMLName.Local != "NonRealTimeMeta" {
		return Media{}
	}
	m := Media{Source: "sony_xml", Make: doc.Device.Manufacturer, Model: doc.Device.Model, Serial: doc.Device.Serial}
	if t, ok := parseTime(doc.CreationDate.Value); ok {
		m.setTime(t)
	}
	m.Width, _ = strconv.Atoi(doc.VideoFormat.Layout.Pixel)
	m.Height, _ = strconv.Atoi(doc.VideoFormat.Layout.Lines)
	return m
}
