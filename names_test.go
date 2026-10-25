package main

import (
	"testing"
	"time"
)

func TestSlugify(t *testing.T) {
	tests := map[string]string{
		"UPI Payment to Ramesh":         "upi-payment-to-ramesh",
		"  Exam Timetable — Sem 5!!  ":  "exam-timetable-sem-5",
		"nil pointer: panic in main.go": "nil-pointer-panic-in-main-go",
		"":                              "screenshot",
		"परीक्षा सूचना":                 "screenshot",
		"a very long title that just keeps going and going forever": "a-very-long-title-that-just-keeps-going-and-going",
	}
	for in, want := range tests {
		if got := Slugify(in); got != want {
			t.Errorf("Slugify(%q) = %q, want %q", in, got, want)
		}
		if got := Slugify(in); len(got) > maxSlugLen {
			t.Errorf("Slugify(%q) too long: %d", in, len(got))
		}
	}
}

func TestDateFromName(t *testing.T) {
	tests := map[string]string{
		"Screenshot from 2026-08-30 23-51-07.png": "2026-08-30",
		"Screenshot_20260831-191418.png":          "2026-08-31",
		"Screenshot 2026-09-01 at 10.02.33.png":   "2026-09-01",
		"/some/dir/IMG_2026.10.05_x.jpg":          "2026-10-05",
	}
	for in, want := range tests {
		got, ok := DateFromName(in)
		if !ok || got.Format("2006-01-02") != want {
			t.Errorf("DateFromName(%q) = %v, %v; want %s", in, got, ok, want)
		}
	}
	for _, in := range []string{"meme.png", "2026-13-45.png", "photo_1234.jpg"} {
		if _, ok := DateFromName(in); ok {
			t.Errorf("DateFromName(%q) should fail", in)
		}
	}
}

func TestSortedName(t *testing.T) {
	d := time.Date(2026, 10, 25, 0, 0, 0, 0, time.UTC)
	if got := SortedName(d, "Hostel Fee Notice", ".PNG"); got != "2026-10-25_hostel-fee-notice.png" {
		t.Errorf("SortedName = %q", got)
	}
}

func TestUniquePath(t *testing.T) {
	taken := map[string]bool{
		"sorted/meme/2026-10-25_cat.png":   true,
		"sorted/meme/2026-10-25_cat-2.png": true,
	}
	isTaken := func(p string) bool { return taken[p] }

	if got := UniquePath("sorted/meme/2026-10-25_dog.png", isTaken); got != "sorted/meme/2026-10-25_dog.png" {
		t.Errorf("free path changed: %q", got)
	}
	if got := UniquePath("sorted/meme/2026-10-25_cat.png", isTaken); got != "sorted/meme/2026-10-25_cat-3.png" {
		t.Errorf("clash = %q, want -3", got)
	}
}
