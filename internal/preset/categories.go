package preset

import (
	"path"
	"strings"
)

// CategoriesVersion is the version of the extension map. Classification
// is by extension, not content verification.
const CategoriesVersion = 1

// Categories.
const (
	CatVideo    = "video"
	CatAudio    = "audio"
	CatImage    = "image"
	CatDocument = "document"
	CatArchive  = "archive"
	CatOther    = "other"
)

var categoryMapV1 = func() map[string]string {
	m := map[string]string{}
	add := func(cat string, exts ...string) {
		for _, e := range exts {
			m[e] = cat
		}
	}
	add(CatVideo, ".mov", ".mp4", ".m4v", ".mxf", ".avi", ".mkv", ".mts", ".m2ts", ".r3d", ".braw", ".ari", ".crm", ".webm", ".mpg", ".mpeg", ".3gp", ".insv", ".360")
	add(CatAudio, ".wav", ".bwf", ".aif", ".aiff", ".mp3", ".m4a", ".flac", ".aac", ".ogg", ".opus", ".caf")
	add(CatImage, ".jpg", ".jpeg", ".png", ".heic", ".heif", ".tif", ".tiff", ".dng", ".arw", ".cr2", ".cr3", ".nef", ".raf", ".orf", ".rw2", ".gif", ".webp", ".psd", ".exr", ".dpx", ".bmp")
	add(CatDocument, ".pdf", ".doc", ".docx", ".txt", ".rtf", ".md", ".pages", ".odt", ".xls", ".xlsx", ".csv", ".numbers", ".key", ".ppt", ".pptx")
	add(CatArchive, ".zip", ".tar", ".gz", ".tgz", ".bz2", ".xz", ".7z", ".rar", ".dmg")
	return m
}()

// CategoryOf classifies a filename by extension (case-insensitive).
func CategoryOf(name string) string {
	if c, ok := categoryMapV1[strings.ToLower(path.Ext(name))]; ok {
		return c
	}
	return CatOther
}

// ValidCategory reports a known category name.
func ValidCategory(c string) bool {
	switch c {
	case CatVideo, CatAudio, CatImage, CatDocument, CatArchive, CatOther:
		return true
	}
	return false
}

// normGlob applies the dialect's separator rules: backslash reads as "/",
// leading/trailing separators are ignored.
func normGlob(g string) string {
	return strings.Trim(strings.ReplaceAll(g, `\`, "/"), "/")
}

// GlobMatch implements Ferry's fixed glob dialect: "/" separators,
// case-insensitive, "*" spans separators (no distinct "**"), "?" one
// character, "[seq]" one of a set, nothing else special.
func GlobMatch(pattern, rel string) bool {
	p := strings.ToLower(normGlob(pattern))
	s := strings.ToLower(strings.Trim(rel, "/"))
	return globMatch(p, s)
}

func globMatch(p, s string) bool {
	for len(p) > 0 {
		switch p[0] {
		case '*':
			for len(p) > 0 && p[0] == '*' {
				p = p[1:]
			}
			if p == "" {
				return true
			}
			for i := 0; i <= len(s); i++ {
				if globMatch(p, s[i:]) {
					return true
				}
			}
			return false
		case '?', '[':
			// path.Match handles one character class or '?' correctly as long
			// as no '/' is involved; '*' spanning is handled above.
			end := 1
			if p[0] == '[' {
				end = strings.IndexByte(p, ']') + 1
				if end <= 0 {
					return false
				}
			}
			if s == "" {
				return false
			}
			_, size := firstRune(s)
			if ok, _ := path.Match(p[:end], s[:size]); !ok {
				return false
			}
			p, s = p[end:], s[size:]
		default:
			if s == "" || p[0] != s[0] {
				return false
			}
			p, s = p[1:], s[1:]
		}
	}
	return s == ""
}

func firstRune(s string) (rune, int) {
	for i, r := range s {
		if i > 0 {
			return r, i
		}
	}
	return rune(s[0]), len(s)
}
