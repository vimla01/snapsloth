package main

import (
	"flag"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"unicode"
)

type hit struct {
	e     *Entry
	score int
}

func cmdSearch(args []string) error {
	fs := flag.NewFlagSet("search", flag.ExitOnError)
	limit := fs.Int("limit", 10, "show at most N matches")
	pos, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	if len(pos) < 2 {
		return fmt.Errorf(`usage: snapsloth search <dir> "<query>"`)
	}
	dir, err := dirArg(pos[:1], 1, `search <dir> "<query>"`)
	if err != nil {
		return err
	}
	terms := strings.Fields(strings.ToLower(strings.Join(pos[1:], " ")))
	if len(terms) == 0 {
		return fmt.Errorf("empty query")
	}
	idx, err := LoadIndex(dir)
	if err != nil {
		return err
	}

	hits := rank(idx.Sorted(), terms, true)
	if len(hits) == 0 {
		hits = rank(idx.Sorted(), terms, false)
		if len(hits) > 0 {
			fmt.Println("no screenshot matches every word; closest matches:")
		}
	}
	if len(hits) == 0 {
		fmt.Printf("nothing matches %q in %d screenshots\n", strings.Join(terms, " "), len(idx.Entries))
		return nil
	}
	for i, h := range hits[:min(*limit, len(hits))] {
		e := h.e
		fmt.Printf("%d. %s  [%s, %s]\n", i+1, e.Title, e.Category, e.Date)
		fmt.Printf("   %s\n", e.Summary)
		if s := snippet(e.Text, terms, 40); s != "" {
			fmt.Printf("   “%s”\n", s)
		}
		fmt.Printf("   %s\n\n", filepath.Join(dir, e.Path))
	}
	if len(hits) > *limit {
		fmt.Printf("…and %d more (--limit %d to see them)\n", len(hits)-*limit, len(hits))
	}
	return nil
}

// rank scores entries for the query terms. Title and category matter most,
// OCR text least, since it is long and noisy. With all set, every term must
// match somewhere.
func rank(entries []*Entry, terms []string, all bool) []hit {
	var hits []hit
	for _, e := range entries {
		title := strings.ToLower(e.Title)
		cat := strings.ReplaceAll(e.Category, "_", " ")
		summary := strings.ToLower(e.Summary)
		text := strings.ToLower(e.Text)
		score, matched := 0, 0
		for _, t := range terms {
			s := 0
			if strings.Contains(title, t) {
				s += 5
			}
			if strings.Contains(cat, t) {
				s += 4
			}
			if strings.Contains(summary, t) {
				s += 3
			}
			s += min(strings.Count(text, t), 3)
			if s > 0 {
				matched++
			}
			score += s
		}
		if matched == 0 || (all && matched < len(terms)) {
			continue
		}
		hits = append(hits, hit{e, score + 2*matched})
	}
	// entries come oldest first; reversing makes ties come out newest first.
	slices.Reverse(hits)
	slices.SortStableFunc(hits, func(a, b hit) int { return b.score - a.score })
	return hits
}

// snippet returns up to width runes either side of the first term found in text.
func snippet(text string, terms []string, width int) string {
	runes := []rune(text)
	lower := make([]rune, len(runes))
	for i, r := range runes {
		lower[i] = unicode.ToLower(r)
	}
	ls := string(lower)
	for _, t := range terms {
		b := strings.Index(ls, t)
		if b < 0 {
			continue
		}
		at := len([]rune(ls[:b]))
		start, end := max(0, at-width), min(len(runes), at+len([]rune(t))+width)
		s := strings.Join(strings.Fields(string(runes[start:end])), " ")
		if start > 0 {
			s = "…" + s
		}
		if end < len(runes) {
			s += "…"
		}
		return s
	}
	return ""
}
