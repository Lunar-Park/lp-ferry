// Package metadatatest builds small, valid media files for tests: real
// container structures, synthetic content. No camera originals are used.
package metadatatest

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"time"
)

func box(typ string, payload ...[]byte) []byte {
	body := bytes.Join(payload, nil)
	out := make([]byte, 8, 8+len(body))
	binary.BigEndian.PutUint32(out[:4], uint32(8+len(body)))
	copy(out[4:8], typ)
	return append(out, body...)
}

func qtText(s string) []byte {
	b := make([]byte, 4, 4+len(s))
	binary.BigEndian.PutUint16(b[:2], uint16(len(s)))
	return append(b, s...)
}

// MP4 is a minimal QuickTime/MP4: ftyp, moov (mvhd creation time, one
// trak with tkhd dimensions, optional udta make/model/date), then an mdat
// of mdatBytes. moovLast puts moov after mdat, as many cameras do.
type MP4 struct {
	Created   time.Time
	Make      string
	Model     string
	Day       string // ©day, e.g. "2026-11-22T10:00:00+0900"
	Width     int
	Height    int
	MdatBytes int
	MoovLast  bool
}

// Bytes renders the file.
func (m MP4) Bytes() []byte {
	mvhd := make([]byte, 100)
	if !m.Created.IsZero() {
		secs := uint32(m.Created.Sub(time.Date(1904, 1, 1, 0, 0, 0, 0, time.UTC)) / time.Second)
		binary.BigEndian.PutUint32(mvhd[4:8], secs)
	}
	tkhd := make([]byte, 84)
	binary.BigEndian.PutUint32(tkhd[76:80], uint32(m.Width)<<16)
	binary.BigEndian.PutUint32(tkhd[80:84], uint32(m.Height)<<16)
	var udta []byte
	var kids [][]byte
	if m.Make != "" {
		kids = append(kids, box("\xa9mak", qtText(m.Make)))
	}
	if m.Model != "" {
		kids = append(kids, box("\xa9mod", qtText(m.Model)))
	}
	if m.Day != "" {
		kids = append(kids, box("\xa9day", qtText(m.Day)))
	}
	if len(kids) > 0 {
		udta = box("udta", kids...)
	}
	moov := box("moov", box("mvhd", mvhd), box("trak", box("tkhd", tkhd)), udta)
	mdat := box("mdat", bytes.Repeat([]byte{0x5A}, m.MdatBytes))
	ftyp := box("ftyp", []byte("XAVC\x00\x00\x00\x00mp42iso2"))
	if m.MoovLast {
		return bytes.Join([][]byte{ftyp, mdat, moov}, nil)
	}
	return bytes.Join([][]byte{ftyp, moov, mdat}, nil)
}

// SonyXML is a NonRealTimeMeta clip sidecar.
func SonyXML(make, model, serial, created string, w, h int) []byte {
	return []byte(fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<NonRealTimeMeta xmlns="urn:schemas-professionalDisc:nonRealTimeMeta:ver.2.20" lastUpdate="%[4]s">
	<Duration value="240"/>
	<CreationDate value="%[4]s"/>
	<VideoFormat><VideoLayout pixel="%[5]d" numOfVerticalLine="%[6]d" aspectRatio="16:9"/></VideoFormat>
	<Device manufacturer="%[1]s" modelName="%[2]s" serialNo="%[3]s"/>
</NonRealTimeMeta>
`, make, model, serial, created, w, h))
}

// WAV is a Broadcast Wave file with bext and optional iXML.
func WAV(originator, date, clock string, ixml string, pcmBytes int) []byte {
	bext := make([]byte, 602)
	copy(bext[256:288], originator)
	copy(bext[320:330], date)
	copy(bext[330:338], clock)
	chunk := func(id string, b []byte) []byte {
		h := make([]byte, 8)
		copy(h, id)
		binary.LittleEndian.PutUint32(h[4:], uint32(len(b)))
		out := append(h, b...)
		if len(b)%2 == 1 {
			out = append(out, 0)
		}
		return out
	}
	fmtChunk := make([]byte, 16)
	binary.LittleEndian.PutUint16(fmtChunk[0:], 1)
	binary.LittleEndian.PutUint16(fmtChunk[2:], 2)
	binary.LittleEndian.PutUint32(fmtChunk[4:], 48000)
	body := [][]byte{[]byte("WAVE"), chunk("fmt ", fmtChunk), chunk("bext", bext)}
	if ixml != "" {
		body = append(body, chunk("iXML", []byte(ixml)))
	}
	body = append(body, chunk("data", make([]byte, pcmBytes)))
	b := bytes.Join(body, nil)
	h := make([]byte, 8)
	copy(h, "RIFF")
	binary.LittleEndian.PutUint32(h[4:], uint32(len(b)))
	return append(h, b...)
}

// JPEG is a JPEG with an EXIF APP1 (Make, Model, DateTimeOriginal,
// OffsetTimeOriginal, BodySerialNumber) and no image data worth decoding.
func JPEG(make, model, dto, offset, serial string) []byte {
	le := binary.LittleEndian
	type ent struct {
		tag uint16
		val string
	}
	var buf bytes.Buffer
	buf.WriteString("II")
	_ = binary.Write(&buf, le, uint16(42))
	_ = binary.Write(&buf, le, uint32(8))
	ifd0 := []ent{{0x010F, make}, {0x0110, model}}
	exif := []ent{{0x9003, dto}, {0x9011, offset}, {0xA431, serial}}
	// Layout: IFD0 at 8, ExifIFD after it, strings after that.
	ifd0Size := 2 + 12*(len(ifd0)+1) + 4
	exifOff := 8 + ifd0Size
	exifSize := 2 + 12*len(exif) + 4
	strOff := exifOff + exifSize
	var strs bytes.Buffer
	writeIFD := func(es []ent, extra func()) {
		n := len(es)
		if extra != nil {
			n++
		}
		_ = binary.Write(&buf, le, uint16(n))
		for _, e := range es {
			v := e.val + "\x00"
			_ = binary.Write(&buf, le, e.tag)
			_ = binary.Write(&buf, le, uint16(2))
			_ = binary.Write(&buf, le, uint32(len(v)))
			_ = binary.Write(&buf, le, uint32(strOff+strs.Len()))
			strs.WriteString(v)
		}
		if extra != nil {
			extra()
		}
		_ = binary.Write(&buf, le, uint32(0))
	}
	writeIFD(ifd0, func() {
		_ = binary.Write(&buf, le, uint16(0x8769))
		_ = binary.Write(&buf, le, uint16(4))
		_ = binary.Write(&buf, le, uint32(1))
		_ = binary.Write(&buf, le, uint32(exifOff))
	})
	writeIFD(exif, nil)
	buf.Write(strs.Bytes())
	tiff := buf.Bytes()
	app1 := append([]byte("Exif\x00\x00"), tiff...)
	out := []byte{0xFF, 0xD8, 0xFF, 0xE1, 0, 0}
	binary.BigEndian.PutUint16(out[4:6], uint16(len(app1)+2))
	out = append(out, app1...)
	return append(out, 0xFF, 0xD9)
}
