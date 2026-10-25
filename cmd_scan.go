package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

func defaultHost() string {
	if h := os.Getenv("OLLAMA_HOST"); h != "" {
		if !strings.Contains(h, "://") {
			h = "http://" + h
		}
		return h
	}
	return "http://localhost:11434"
}

func cmdScan(args []string) error {
	fs := flag.NewFlagSet("scan", flag.ExitOnError)
	model := fs.String("model", "gemma4:e4b", "Ollama vision model")
	host := fs.String("host", defaultHost(), "Ollama URL")
	limit := fs.Int("limit", 0, "analyse at most N new screenshots (0 = all)")
	dryRun := fs.Bool("dry-run", false, "list new screenshots without calling the model")
	pos, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	dir, err := dirArg(pos, 1, "scan <dir>")
	if err != nil {
		return err
	}

	idx, err := LoadIndex(dir)
	if err != nil {
		return err
	}
	files, err := FindImages(dir)
	if err != nil {
		return err
	}

	// Hashing is cheap next to the model, and it means renamed or sorted
	// files are recognised instead of being analysed again.
	type job struct{ rel, hash string }
	var todo []job
	var known, dups int
	seen := map[string]bool{}
	for _, rel := range files {
		h, err := hashFile(filepath.Join(dir, rel))
		if err != nil {
			fmt.Fprintf(os.Stderr, "skip %s: %v\n", rel, err)
			continue
		}
		if e, ok := idx.Entries[h]; ok {
			e.Path = rel // follow files the user moved by hand
			known++
			continue
		}
		if seen[h] {
			dups++ // exact copy of a file already queued
			continue
		}
		seen[h] = true
		todo = append(todo, job{rel, h})
	}
	fmt.Printf("%d screenshots: %d already indexed, %d new", len(files), known, len(todo))
	if dups > 0 {
		fmt.Printf(", %d exact duplicates skipped", dups)
	}
	fmt.Println()
	if *limit > 0 && len(todo) > *limit {
		todo = todo[:*limit]
		fmt.Printf("--limit: analysing the first %d\n", *limit)
	}
	if len(todo) == 0 {
		return idx.Save()
	}
	if *dryRun {
		for _, j := range todo {
			fmt.Println("  would scan", j.rel)
		}
		return nil
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	client := NewOllamaClient(*host, *model)
	if err := client.Check(ctx); err != nil {
		return err
	}
	fmt.Printf("model %s on %s (CPU: expect 10-30 s per image; Ctrl+C is safe)\n\n", *model, *host)

	var done, failed int
	var total time.Duration
	for i, j := range todo {
		path := filepath.Join(dir, j.rel)
		img, err := os.ReadFile(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[%d/%d] %s: %v\n", i+1, len(todo), j.rel, err)
			failed++
			continue
		}
		start := time.Now()
		a, err := client.Analyze(ctx, img)
		took := time.Since(start)
		if ctx.Err() != nil {
			fmt.Printf("\ninterrupted. %d saved, run scan again to continue.\n", done)
			return nil
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "[%d/%d] %s: %v\n", i+1, len(todo), j.rel, err)
			failed++
			continue
		}

		e := newEntry(a, j.hash, j.rel, captureDate(path), *model, took)
		idx.Entries[j.hash] = e
		if err := idx.Save(); err != nil {
			return err
		}
		done++
		total += took

		flag := ""
		if e.Sensitive {
			flag = "  ⚠ " + strings.Join(e.SensitiveTypes, ",")
		}
		fmt.Printf("[%d/%d] %5.1fs  %-17s %s%s\n", i+1, len(todo), took.Seconds(), e.Category, e.Title, flag)
	}

	fmt.Printf("\ndone: %d analysed", done)
	if done > 0 {
		fmt.Printf(", %.1f s/image avg, %s total", total.Seconds()/float64(done), total.Round(time.Second))
	}
	if failed > 0 {
		fmt.Printf(", %d failed (will retry next scan)", failed)
	}
	fmt.Println()
	if failed > 0 && done == 0 {
		return errors.New("every screenshot failed; see errors above")
	}
	return nil
}

// newEntry merges the model's answer with the regex backup and masks every
// sensitive value before anything is stored.
func newEntry(a *Analysis, hash, rel, date, model string, took time.Duration) *Entry {
	text, findings := Scrub(a.Text)
	title, f2 := Scrub(a.Title)
	summary, f3 := Scrub(a.Summary)
	for _, f := range append(f2, f3...) {
		if !slices.Contains(findings, f) {
			findings = append(findings, f)
		}
	}

	types := slices.Clone(a.SensitiveTypes)
	for _, t := range FindingTypes(findings) {
		if !slices.Contains(types, t) {
			types = append(types, t)
		}
	}
	return &Entry{
		Hash:           hash,
		Path:           rel,
		OrigName:       filepath.Base(rel),
		Date:           date,
		Category:       a.Category,
		Title:          strings.TrimSpace(title),
		Summary:        strings.TrimSpace(summary),
		Text:           text,
		Sensitive:      a.Sensitive || len(findings) > 0,
		SensitiveTypes: types,
		Findings:       findings,
		Model:          model,
		Seconds:        took.Seconds(),
		ScannedAt:      time.Now(),
	}
}
