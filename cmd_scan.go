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

type scanJob struct{ rel, hash string }

// ScanPlan is what a scan would do: which files are new and which are known.
type ScanPlan struct {
	Files, Known, Dups int
	Removed            int // index entries whose file was deleted
	Todo               []scanJob
}

// ScanEvent reports progress on one screenshot.
type ScanEvent struct {
	I, N    int
	Rel     string
	Entry   *Entry // nil if Err is set
	Seconds float64
	Err     error
}

// planScan hashes every image and works out which are new. Hashing is cheap
// next to the model, and it means renamed or sorted files are recognised
// instead of being analysed again. Known entries get their path refreshed.
func planScan(dir string, idx *Index) (*ScanPlan, error) {
	files, err := FindImages(dir)
	if err != nil {
		return nil, err
	}
	p := &ScanPlan{Files: len(files)}
	seen := map[string]bool{}
	onDisk := map[string]bool{}
	for _, rel := range files {
		h, err := hashFile(filepath.Join(dir, rel))
		if err != nil {
			fmt.Fprintf(os.Stderr, "skip %s: %v\n", rel, err)
			continue
		}
		onDisk[h] = true
		if e, ok := idx.Entries[h]; ok {
			e.Path = rel // follow files the user moved by hand
			safetyCheck(e)
			p.Known++
			continue
		}
		if seen[h] {
			p.Dups++ // exact copy of a file already queued
			continue
		}
		seen[h] = true
		p.Todo = append(p.Todo, scanJob{rel, h})
	}
	// Newest first: recent screenshots are the ones still worth acting on,
	// so the report is useful long before a big backlog is done.
	dates := map[string]string{}
	for _, j := range p.Todo {
		dates[j.rel] = captureDate(filepath.Join(dir, j.rel))
	}
	slices.SortStableFunc(p.Todo, func(a, b scanJob) int {
		if c := strings.Compare(dates[b.rel], dates[a.rel]); c != 0 {
			return c
		}
		return strings.Compare(b.rel, a.rel)
	})

	// Forget screenshots the user deleted, so they stop showing up.
	for h, e := range idx.Entries {
		if !onDisk[h] && !exists(filepath.Join(dir, e.Path)) {
			delete(idx.Entries, h)
			p.Removed++
		}
	}
	return p, nil
}

// runScan analyses todo one by one, saving the index after every image so a
// crash or Ctrl+C loses nothing. It stops early when ctx is cancelled.
func runScan(ctx context.Context, dir string, idx *Index, client *OllamaClient, todo []scanJob, emit func(ScanEvent)) error {
	for i, j := range todo {
		ev := ScanEvent{I: i + 1, N: len(todo), Rel: j.rel}
		path := filepath.Join(dir, j.rel)
		img, err := os.ReadFile(path)
		if err != nil {
			ev.Err = err
			emit(ev)
			continue
		}
		start := time.Now()
		a, err := client.Analyze(ctx, img)
		took := time.Since(start)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		ev.Seconds = took.Seconds()
		if err != nil {
			ev.Err = err
			emit(ev)
			continue
		}
		e := newEntry(a, j.hash, j.rel, captureDate(path), client.Model, took)
		if err := idx.Put(e); err != nil {
			return err
		}
		ev.Entry = e
		emit(ev)
	}
	return nil
}

func cmdScan(args []string) error {
	fs := flag.NewFlagSet("scan", flag.ExitOnError)
	model := fs.String("model", "gemma4:e4b", "Ollama vision model")
	host := fs.String("host", defaultHost(), "Ollama URL")
	limit := fs.Int("limit", 0, "analyse at most N new screenshots (0 = all)")
	dryRun := fs.Bool("dry-run", false, "list new screenshots without calling the model")
	imageSize := fs.Int("image-size", defaultImageSize, "shrink images to this many pixels on the longest side (0 = full size; smaller is faster)")
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
	plan, err := planScan(dir, idx)
	if err != nil {
		return err
	}
	if !*dryRun {
		if err := idx.Save(); err != nil {
			return err
		}
	}
	todo := plan.Todo
	fmt.Printf("%d screenshots: %d already indexed, %d new", plan.Files, plan.Known, len(todo))
	if plan.Dups > 0 {
		fmt.Printf(", %d exact duplicates skipped", plan.Dups)
	}
	if plan.Removed > 0 {
		fmt.Printf(", %d deleted ones forgotten", plan.Removed)
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
	client.ImageSize = *imageSize
	if err := client.Check(ctx); err != nil {
		return err
	}
	fmt.Printf("model %s on %s (CPU: about a minute per image; Ctrl+C is safe)\n\n", *model, *host)

	var done, failed int
	var total float64
	err = runScan(ctx, dir, idx, client, todo, func(ev ScanEvent) {
		if ev.Err != nil {
			fmt.Fprintf(os.Stderr, "[%d/%d] %s: %v\n", ev.I, ev.N, ev.Rel, ev.Err)
			failed++
			return
		}
		done++
		total += ev.Seconds
		e := ev.Entry
		flag := ""
		if e.Sensitive {
			flag = "  ⚠ " + strings.Join(e.SensitiveTypes, ",")
		}
		fmt.Printf("[%d/%d] %5.1fs  %-17s %s%s\n", ev.I, ev.N, ev.Seconds, e.Category, e.Title, flag)
	})
	if errors.Is(err, context.Canceled) {
		fmt.Printf("\ninterrupted. %d saved, run scan again to continue.\n", done)
		return nil
	}
	if err != nil {
		return err
	}

	fmt.Printf("\ndone: %d analysed", done)
	if done > 0 {
		fmt.Printf(", %.1f s/image avg, %s total", total/float64(done), time.Duration(total*float64(time.Second)).Round(time.Second))
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

// newEntry turns the model's answer into an index entry, with every
// sensitive value masked before anything is stored.
func newEntry(a *Analysis, hash, rel, date, model string, took time.Duration) *Entry {
	e := &Entry{
		Hash:           hash,
		Path:           rel,
		OrigName:       filepath.Base(rel),
		Date:           date,
		Category:       a.Category,
		Title:          strings.TrimSpace(a.Title),
		Summary:        strings.TrimSpace(a.Summary),
		Text:           a.Text,
		Todo:           cleanTodo(a.Todo),
		Sensitive:      a.Sensitive,
		SensitiveTypes: slices.Clone(a.SensitiveTypes),
		Model:          model,
		Seconds:        took.Seconds(),
		ScannedAt:      time.Now(),
	}
	safetyCheck(e)
	return e
}

// safetyCheck runs the regex backup over an entry: masks values, adds what it
// found, and files ID cards as ID documents. Masked text doesn't match again,
// so it's safe to rerun on old entries whenever detection improves.
func safetyCheck(e *Entry) {
	for _, field := range []*string{&e.Text, &e.Title, &e.Summary, &e.Todo} {
		masked, found := Scrub(*field)
		*field = masked
		for _, f := range found {
			if !slices.Contains(e.Findings, f) {
				e.Findings = append(e.Findings, f)
			}
		}
	}
	// An ID card is sensitive even when its number isn't visible, and it
	// belongs with the other ID documents whatever the model called it
	// (unless the user picked a category themselves).
	if kind := IDCardKind(e.Text + " " + e.Title + " " + e.Summary); kind != "" {
		if !e.Reviewed && e.Category != "id_document" {
			e.ModelCategory, e.Category = e.Category, "id_document"
		}
		if !slices.ContainsFunc(e.Findings, func(x Finding) bool { return x.Type == kind }) {
			e.Findings = append(e.Findings, Finding{Type: kind, Masked: "ID card with name and date of birth"})
		}
	}
	for _, t := range FindingTypes(e.Findings) {
		if !slices.Contains(e.SensitiveTypes, t) {
			e.SensitiveTypes = append(e.SensitiveTypes, t)
		}
	}
	e.Sensitive = e.Sensitive || len(e.Findings) > 0
}

// cleanTodo drops the ways a model says "nothing to do" without leaving the
// field empty.
func cleanTodo(s string) string {
	s = strings.TrimSpace(s)
	switch strings.ToLower(strings.Trim(s, ".!- ")) {
	case "", "none", "null", "nil", "n/a", "na", "no", "nothing", "no action", "no action needed", "not applicable":
		return ""
	}
	return s
}
