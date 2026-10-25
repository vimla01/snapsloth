package main

import (
	"flag"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// How urgent each kind of sensitive data is to get rid of.
var riskAdvice = map[string]string{
	"otp":          "OTPs expire in minutes; nothing to keep",
	"password":     "move it to a password manager, then delete",
	"bank_details": "delete unless you need it; keep in a locked folder if so",
	"aadhaar":      "ID scans are prime fraud material; keep the original only in DigiLocker",
	"pan":          "ID scans are prime fraud material; keep the original only in DigiLocker",
	"upi_id":       "mostly harmless alone, but shows who you pay",
	"phone":        "someone's number; fine if it's yours",
	"address":      "a home address; delete if it's not needed",
}

func cmdRisky(args []string) error {
	fs := flag.NewFlagSet("risky", flag.ExitOnError)
	pos, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	dir, err := dirArg(pos, 1, "risky <dir>")
	if err != nil {
		return err
	}
	idx, err := LoadIndex(dir)
	if err != nil {
		return err
	}

	var risky []*Entry
	for _, e := range idx.Sorted() {
		if e.Sensitive {
			risky = append(risky, e)
		}
	}
	if len(risky) == 0 {
		fmt.Printf("no risky screenshots among %d indexed 🎉\n", len(idx.Entries))
		return nil
	}

	fmt.Printf("⚠ %d of %d screenshots contain sensitive info\n\n", len(risky), len(idx.Entries))
	seenAdvice := map[string]bool{}
	for _, e := range risky {
		fmt.Printf("• %s  [%s, %s]\n", e.Title, e.Category, e.Date)
		fmt.Printf("  %s\n", filepath.Join(dir, e.Path))
		fmt.Printf("  why: %s\n", riskReason(e))
		for _, t := range e.SensitiveTypes {
			if a, ok := riskAdvice[t]; ok && !seenAdvice[t] {
				seenAdvice[t] = true
				fmt.Printf("  tip: %s\n", a)
			}
		}
		fmt.Println()
	}
	fmt.Println("Suggestion: review these and delete the ones you don't need.")
	fmt.Println("SnapSloth never deletes anything itself. Open the folder in your file manager:")
	fmt.Printf("  %s\n", dir)
	return nil
}

// riskReason explains a flag using only masked values.
func riskReason(e *Entry) string {
	var parts []string
	for _, t := range e.SensitiveTypes {
		var vals []string
		for _, f := range e.Findings {
			if f.Type == t && !slices.Contains(vals, f.Masked) {
				vals = append(vals, f.Masked)
			}
		}
		if len(vals) > 0 {
			parts = append(parts, fmt.Sprintf("%s (%s)", t, strings.Join(vals, ", ")))
		} else {
			parts = append(parts, t+" (spotted by the model)")
		}
	}
	if len(parts) == 0 {
		return "the model marked it sensitive"
	}
	return strings.Join(parts, "; ")
}

// Categories that usually mean "I'll deal with this later", and words that
// suggest it still needs doing.
var (
	forgetWeight = map[string]int{
		"college_notice": 4, "upi_payment": 3, "ticket_or_booking": 3,
		"timetable": 2, "shopping": 2,
	}
	todoWords = []string{
		"due", "deadline", "last date", "pending", "request", "submit", "fee",
		"exam", "tomorrow", "pay ", "collect", "register", "before", "remind",
		"failed", "refund", "return", "pnr", "departure", "check-in", "assignment",
	}
)

func forgetScore(e *Entry) int {
	// The model's own to-do beats any keyword guess.
	if e.Todo != "" {
		return 20 + forgetWeight[e.Category]
	}
	w, ok := forgetWeight[e.Category]
	if !ok {
		return 0
	}
	hay := strings.ToLower(e.Title + " " + e.Summary + " " + e.Text)
	hits := 0
	for _, word := range todoWords {
		if strings.Contains(hay, word) {
			hits++
		}
	}
	return w*2 + min(hits, 4)*3
}

// CatCount is one row of the per-category breakdown.
type CatCount struct {
	Category string `json:"category"`
	Count    int    `json:"count"`
}

// Report is the morning summary, shared by the CLI and the UI.
type Report struct {
	Total        int        `json:"total"`
	LastDay      int        `json:"last_day"`
	Risky        int        `json:"risky"`
	ModelSeconds float64    `json:"model_seconds"`
	Categories   []CatCount `json:"categories"` // biggest first, empty ones left out
	Forgotten    []*Entry   `json:"forgotten"`  // top 3
}

func buildReport(idx *Index, now time.Time) Report {
	r := Report{Categories: []CatCount{}, Forgotten: []*Entry{}}
	counts := map[string]int{}
	var forgotten []*Entry
	for _, e := range idx.Sorted() {
		r.Total++
		counts[e.Category]++
		r.ModelSeconds += e.Seconds
		if e.Sensitive {
			r.Risky++
		}
		if now.Sub(e.ScannedAt) < 24*time.Hour {
			r.LastDay++
		}
		if forgetScore(e) >= 8 {
			forgotten = append(forgotten, e)
		}
	}
	for _, c := range categories {
		if counts[c] > 0 {
			r.Categories = append(r.Categories, CatCount{c, counts[c]})
		}
	}
	slices.SortStableFunc(r.Categories, func(a, b CatCount) int { return b.Count - a.Count })
	// Highest score first; older first among equals, since those are the
	// most forgotten.
	slices.SortStableFunc(forgotten, func(a, b *Entry) int { return forgetScore(b) - forgetScore(a) })
	r.Forgotten = append(r.Forgotten, forgotten[:min(3, len(forgotten))]...)
	return r
}

func cmdReport(args []string) error {
	fs := flag.NewFlagSet("report", flag.ExitOnError)
	pos, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	dir, err := dirArg(pos, 1, "report <dir>")
	if err != nil {
		return err
	}
	idx, err := LoadIndex(dir)
	if err != nil {
		return err
	}
	now := time.Now()
	r := buildReport(idx, now)

	fmt.Printf("🦥 SnapSloth morning report, %s\n", now.Format("Mon 2 Jan 2006"))
	fmt.Printf("   %s\n\n", dir)
	if r.Total == 0 {
		fmt.Println("nothing indexed yet; run: snapsloth scan", dir)
		return nil
	}
	fmt.Printf("Processed:   %d screenshots (%d new in the last 24 h)\n", r.Total, r.LastDay)
	fmt.Printf("Model time:  %s total, %.1f s per image\n", time.Duration(r.ModelSeconds*float64(time.Second)).Round(time.Second), r.ModelSeconds/float64(r.Total))
	fmt.Printf("Risky:       %d  → snapsloth risky %q\n\n", r.Risky, dir)

	fmt.Println("By category:")
	for _, c := range r.Categories {
		fmt.Printf("  %-18s %4d  %s\n", c.Category, c.Count, strings.Repeat("▇", max(1, c.Count*30/r.Total)))
	}
	if len(r.Forgotten) > 0 {
		fmt.Println("\nThings you saved and probably forgot:")
		for i, e := range r.Forgotten {
			fmt.Printf("  %d. %s  [%s, %s]\n", i+1, e.Title, e.Category, e.Date)
			if e.Todo != "" {
				fmt.Printf("     → %s\n", e.Todo)
			} else {
				fmt.Printf("     %s\n", e.Summary)
			}
			fmt.Printf("     %s\n", filepath.Join(dir, e.Path))
		}
	}
	fmt.Println("\nGo drink your chai. ☕")
	return nil
}
