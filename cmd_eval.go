package main

import (
	"encoding/csv"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

type labelResult struct {
	file, want, got, title string
}

func cmdEval(args []string) error {
	fs := flag.NewFlagSet("eval", flag.ExitOnError)
	initCSV := fs.Bool("init", false, "write labels.csv listing every indexed file with a blank category, for you to fill in")
	pos, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	if len(pos) < 1 || len(pos) > 2 || (*initCSV && len(pos) != 2) {
		return errors.New("usage: snapsloth eval <dir> [labels.csv] [--init]")
	}
	dir, err := dirArg(pos[:1], 1, "eval <dir> [labels.csv]")
	if err != nil {
		return err
	}
	idx, err := LoadIndex(dir)
	if err != nil {
		return err
	}
	if len(pos) == 1 {
		return evalReviewed(idx)
	}
	if *initCSV {
		return writeLabelTemplate(pos[1], idx)
	}

	labels, err := readLabels(pos[1])
	if err != nil {
		return err
	}
	// Labels can name a file by its original name or its sorted name.
	byName := map[string]*Entry{}
	for _, e := range idx.Entries {
		byName[e.OrigName] = e
		byName[filepath.Base(e.Path)] = e
	}

	var results []labelResult
	var notScanned []string
	var seconds float64
	for _, l := range labels {
		e, ok := byName[filepath.Base(l[0])]
		if !ok {
			notScanned = append(notScanned, l[0])
			continue
		}
		results = append(results, labelResult{l[0], l[1], e.ModelSaid(), e.Title})
		seconds += e.Seconds
	}
	if len(results) == 0 {
		return errors.New("none of the labelled files are in the index; run scan first")
	}
	printEval(results, seconds)
	if len(notScanned) > 0 {
		fmt.Printf("\n%d labelled files aren't indexed yet (scan them first): %s\n", len(notScanned), strings.Join(notScanned[:min(5, len(notScanned))], ", "))
	}
	return nil
}

// evalReviewed scores the model against the categories the user checked or
// fixed in the UI.
func evalReviewed(idx *Index) error {
	var results []labelResult
	var seconds float64
	for _, e := range idx.Sorted() {
		if e.Reviewed {
			results = append(results, labelResult{e.OrigName, e.Category, e.ModelSaid(), e.Title})
			seconds += e.Seconds
		}
	}
	if len(results) == 0 {
		return errors.New("no reviewed screenshots yet: open `snapsloth ui`, click screenshots and press \"Looks right\" or fix the category (or pass a labels.csv)")
	}
	fmt.Printf("Using the %d screenshots you checked in the UI.\n\n", len(results))
	printEval(results, seconds)
	return nil
}

func printEval(results []labelResult, seconds float64) {
	type stat struct{ labelled, predicted, correct int }
	stats := map[string]*stat{}
	for _, c := range categories {
		stats[c] = &stat{}
	}
	confused := map[[2]string]int{}
	var wrong []labelResult
	correct := 0
	for _, r := range results {
		stats[r.want].labelled++
		stats[r.got].predicted++
		if r.want == r.got {
			stats[r.want].correct++
			correct++
		} else {
			confused[[2]string{r.want, r.got}]++
			wrong = append(wrong, r)
		}
	}

	n := len(results)
	fmt.Printf("Accuracy: %d/%d = %.1f%%   (%.1f s/image avg)\n\n", correct, n, pct(correct, n), seconds/float64(n))

	// Markdown, so it pastes straight into the blog post.
	fmt.Println("| category | labelled | correct | recall | precision |")
	fmt.Println("|---|---:|---:|---:|---:|")
	for _, c := range categories {
		s := stats[c]
		if s.labelled == 0 && s.predicted == 0 {
			continue
		}
		prec := "–"
		if s.predicted > 0 {
			prec = fmt.Sprintf("%.0f%%", pct(s.correct, s.predicted))
		}
		rec := "–"
		if s.labelled > 0 {
			rec = fmt.Sprintf("%.0f%%", pct(s.correct, s.labelled))
		}
		fmt.Printf("| %s | %d | %d | %s | %s |\n", c, s.labelled, s.correct, rec, prec)
	}

	if len(confused) > 0 {
		pairs := make([][2]string, 0, len(confused))
		for p := range confused {
			pairs = append(pairs, p)
		}
		slices.SortFunc(pairs, func(a, b [2]string) int {
			if d := confused[b] - confused[a]; d != 0 {
				return d
			}
			return strings.Compare(a[0]+a[1], b[0]+b[1])
		})
		fmt.Println("\nMost common mix-ups (true → predicted):")
		for _, p := range pairs {
			fmt.Printf("  %-18s → %-18s %d\n", p[0], p[1], confused[p])
		}

		fmt.Printf("\nMisclassified (%d):\n", len(wrong))
		for _, r := range wrong {
			fmt.Printf("  %s\n    expected %s, got %s (%q)\n", r.file, r.want, r.got, r.title)
		}
	}
}

func pct(a, b int) float64 { return 100 * float64(a) / float64(b) }

// readLabels parses filename,category rows, skipping a header and rows whose
// category is still blank.
func readLabels(path string) ([][2]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	r := csv.NewReader(f)
	r.FieldsPerRecord = -1
	r.TrimLeadingSpace = true
	var out [][2]string
	for line := 1; ; line++ {
		rec, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		if len(rec) < 2 || (line == 1 && strings.EqualFold(rec[0], "filename")) {
			continue
		}
		name, cat := strings.TrimSpace(rec[0]), strings.ToLower(strings.TrimSpace(rec[1]))
		if name == "" || cat == "" {
			continue
		}
		if !slices.Contains(categories, cat) {
			return nil, fmt.Errorf("%s:%d: unknown category %q (use one of: %s)", path, line, cat, strings.Join(categories, ", "))
		}
		out = append(out, [2]string{name, cat})
	}
	if len(out) == 0 {
		return nil, errors.New(path + " has no labelled rows")
	}
	return out, nil
}

// writeLabelTemplate lists files with blank categories rather than the model's
// guesses, so labelling isn't nudged towards agreeing with the model.
func writeLabelTemplate(path string, idx *Index) error {
	if _, err := os.Stat(path); !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("%s already exists, not overwriting", path)
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	w := csv.NewWriter(f)
	// Alphabetical, to match the order a file manager shows them in.
	var names []string
	for _, e := range idx.Entries {
		names = append(names, e.OrigName)
	}
	slices.Sort(names)
	w.Write([]string{"filename", "category"})
	for _, name := range names {
		w.Write([]string{name, ""})
	}
	w.Flush()
	if err := w.Error(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	fmt.Printf("wrote %s with %d files. Fill in the category column (blank rows are skipped), using:\n  %s\n",
		path, len(idx.Entries), strings.Join(categories, ", "))
	return nil
}
