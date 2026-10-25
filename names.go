package main

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const maxSlugLen = 50

var (
	reNonSlug = regexp.MustCompile(`[^a-z0-9]+`)
	// Matches the date in "Screenshot from 2026-08-30 23-51-07.png",
	// "Screenshot_20260830-235107.png", "Screenshot 2026-08-30 at 23.51.07.png", ...
	reNameDate = regexp.MustCompile(`(20\d{2})[-_.]?(0[1-9]|1[0-2])[-_.]?(0[1-9]|[12]\d|3[01])`)
)

// Slugify turns a model title into a safe, lowercase, hyphenated file name part.
func Slugify(title string) string {
	s := strings.Trim(reNonSlug.ReplaceAllString(strings.ToLower(title), "-"), "-")
	if len(s) > maxSlugLen {
		s = s[:maxSlugLen]
		// Prefer cutting at a word boundary over leaving half a word.
		if i := strings.LastIndexByte(s, '-'); i > maxSlugLen/2 {
			s = s[:i]
		}
		s = strings.TrimRight(s, "-")
	}
	if s == "" {
		return "screenshot"
	}
	return s
}

// DateFromName pulls the capture date out of a screenshot's file name.
func DateFromName(name string) (time.Time, bool) {
	m := reNameDate.FindStringSubmatch(filepath.Base(name))
	if m == nil {
		return time.Time{}, false
	}
	t, err := time.Parse("2006-01-02", m[1]+"-"+m[2]+"-"+m[3])
	return t, err == nil
}

// SortedName builds "YYYY-MM-DD_<slug>.<ext>".
func SortedName(date time.Time, title, ext string) string {
	return date.Format("2006-01-02") + "_" + Slugify(title) + strings.ToLower(ext)
}

// UniquePath returns path, or path with -2, -3, ... before the extension,
// whichever is the first that taken reports as free.
func UniquePath(path string, taken func(string) bool) string {
	if !taken(path) {
		return path
	}
	ext := filepath.Ext(path)
	stem := strings.TrimSuffix(path, ext)
	for n := 2; ; n++ {
		p := fmt.Sprintf("%s-%d%s", stem, n, ext)
		if !taken(p) {
			return p
		}
	}
}
