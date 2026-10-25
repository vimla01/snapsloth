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
	fmt.Println("SnapSloth never deletes anything itself. Open the folder with:")
	fmt.Printf("  xdg-open %q\n", dir)
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
		"timetable": 2, "shopping": 2, "bank_or_otp": 1, "notes_or_study": 1,
	}
	todoWords = []string{
		"due", "deadline", "last date", "pending", "request", "submit", "fee",
		"exam", "tomorrow", "pay ", "collect", "register", "before", "remind",
		"failed", "refund", "return", "pnr", "departure", "check-in", "assignment",
	}
)

func forgetScore(e *Entry) int {
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
	entries := idx.Sorted()
	now := time.Now()

	var lastNight, risky int
	var modelTime float64
	counts := map[string]int{}
	for _, e := range entries {
		counts[e.Category]++
		modelTime += e.Seconds
		if e.Sensitive {
			risky++
		}
		if now.Sub(e.ScannedAt) < 24*time.Hour {
			lastNight++
		}
	}

	fmt.Printf("🦥 SnapSloth morning report, %s\n", now.Format("Mon 2 Jan 2006"))
	fmt.Printf("   %s\n\n", dir)
	if len(entries) == 0 {
		fmt.Println("nothing indexed yet; run: snapsloth scan", dir)
		return nil
	}
	fmt.Printf("Processed:   %d screenshots (%d new in the last 24 h)\n", len(entries), lastNight)
	fmt.Printf("Model time:  %s total, %.1f s per image\n", time.Duration(modelTime*float64(time.Second)).Round(time.Second), modelTime/float64(len(entries)))
	fmt.Printf("Risky:       %d  → snapsloth risky %q\n\n", risky, dir)

	fmt.Println("By category:")
	cats := slices.Clone(categories)
	slices.SortStableFunc(cats, func(a, b string) int { return counts[b] - counts[a] })
	for _, c := range cats {
		if counts[c] > 0 {
			fmt.Printf("  %-18s %4d  %s\n", c, counts[c], strings.Repeat("▇", max(1, counts[c]*30/len(entries))))
		}
	}

	var forgotten []*Entry
	for _, e := range entries {
		if forgetScore(e) >= 8 {
			forgotten = append(forgotten, e)
		}
	}
	// Highest score first; older first among equals, since those are the
	// most forgotten.
	slices.SortStableFunc(forgotten, func(a, b *Entry) int { return forgetScore(b) - forgetScore(a) })
	if len(forgotten) > 0 {
		fmt.Println("\nThings you saved and probably forgot:")
		for i, e := range forgotten[:min(3, len(forgotten))] {
			fmt.Printf("  %d. %s  [%s, %s]\n", i+1, e.Title, e.Category, e.Date)
			fmt.Printf("     %s\n", e.Summary)
			fmt.Printf("     %s\n", filepath.Join(dir, e.Path))
		}
	}
	fmt.Println("\nGo drink your chai. ☕")
	return nil
}
