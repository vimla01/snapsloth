package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

const sortedDir = "sorted"

// Move is one file rename, with paths relative to the screenshots folder.
type Move struct {
	Hash string `json:"hash"`
	From string `json:"from"`
	To   string `json:"to"`
}

// SortRun is everything one `sort` did, so `undo` can reverse it as a unit.
type SortRun struct {
	At     time.Time `json:"at"`
	Moves  []Move    `json:"moves"`
	Undone bool      `json:"undone"`
}

type MoveLog struct {
	Runs []*SortRun `json:"runs"`
	path string
}

func loadMoveLog(dir string) (*MoveLog, error) {
	l := &MoveLog{path: filepath.Join(dir, stateDir, "moves.json")}
	data, err := os.ReadFile(l.path)
	if errors.Is(err, fs.ErrNotExist) {
		return l, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, l); err != nil {
		return nil, errors.New("corrupt move log " + l.path + ": " + err.Error())
	}
	return l, nil
}

func (l *MoveLog) Save() error { return writeJSONAtomic(l.path, l) }

func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

// moveNoClobber renames from to to, refusing to replace an existing file.
func moveNoClobber(from, to string) error {
	if exists(to) {
		return fmt.Errorf("%s already exists, not overwriting", to)
	}
	if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
		return err
	}
	return os.Rename(from, to)
}

func cmdSort(args []string) error {
	fs := flag.NewFlagSet("sort", flag.ExitOnError)
	dryRun := fs.Bool("dry-run", false, "show what would move without moving anything")
	pos, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	dir, err := dirArg(pos, 1, "sort <dir> [--dry-run]")
	if err != nil {
		return err
	}
	idx, err := LoadIndex(dir)
	if err != nil {
		return err
	}
	if len(idx.Entries) == 0 {
		return errors.New("nothing indexed yet; run: snapsloth scan " + dir)
	}
	log, err := loadMoveLog(dir)
	if err != nil {
		return err
	}

	run := &SortRun{At: time.Now()}
	claimed := map[string]bool{} // targets taken earlier in this run (matters for --dry-run)
	taken := func(rel string) bool { return claimed[rel] || exists(filepath.Join(dir, rel)) }
	var missing, already int

	for _, e := range idx.Sorted() {
		if !exists(filepath.Join(dir, e.Path)) {
			missing++
			continue
		}
		// Already filed in the right category folder: leave it alone, so
		// sorting twice doesn't rename anything.
		if filepath.Dir(e.Path) == filepath.Join(sortedDir, e.Category) {
			already++
			continue
		}
		want := filepath.Join(sortedDir, e.Category, SortedName(parseDate(e.Date), e.Title, filepath.Ext(e.Path)))
		to := UniquePath(want, taken)
		claimed[to] = true

		if *dryRun {
			fmt.Printf("%s\n  → %s\n", e.Path, to)
			run.Moves = append(run.Moves, Move{e.Hash, e.Path, to})
			continue
		}
		if err := moveNoClobber(filepath.Join(dir, e.Path), filepath.Join(dir, to)); err != nil {
			fmt.Fprintln(os.Stderr, "skip:", err)
			continue
		}
		if len(run.Moves) == 0 {
			log.Runs = append(log.Runs, run)
		}
		run.Moves = append(run.Moves, Move{e.Hash, e.Path, to})
		e.Path = to
		// Log the move before touching the index: if we crash in between,
		// undo still knows where the file went.
		if err := log.Save(); err != nil {
			return err
		}
		if err := idx.Save(); err != nil {
			return err
		}
		fmt.Printf("%s → %s\n", run.Moves[len(run.Moves)-1].From, to)
	}

	verb := "moved"
	if *dryRun {
		verb = "would move"
	}
	fmt.Printf("\n%s %d files", verb, len(run.Moves))
	if already > 0 {
		fmt.Printf(", %d already sorted", already)
	}
	if missing > 0 {
		fmt.Printf(", %d indexed files not found on disk", missing)
	}
	fmt.Println()
	if !*dryRun && len(run.Moves) > 0 {
		fmt.Println("changed your mind? snapsloth undo", dir)
	}
	return nil
}

func cmdUndo(args []string) error {
	fs := flag.NewFlagSet("undo", flag.ExitOnError)
	dryRun := fs.Bool("dry-run", false, "show what would move back without moving anything")
	pos, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	dir, err := dirArg(pos, 1, "undo <dir> [--dry-run]")
	if err != nil {
		return err
	}
	log, err := loadMoveLog(dir)
	if err != nil {
		return err
	}
	var run *SortRun
	for j := len(log.Runs) - 1; j >= 0; j-- {
		if !log.Runs[j].Undone {
			run = log.Runs[j]
			break
		}
	}
	if run == nil {
		return errors.New("no sort to undo")
	}
	idx, err := LoadIndex(dir)
	if err != nil {
		return err
	}

	fmt.Printf("undoing sort from %s (%d moves)\n", run.At.Format("Mon 2 Jan 15:04"), len(run.Moves))
	var back, skipped int
	for _, m := range slices.Backward(run.Moves) {
		if *dryRun {
			fmt.Printf("%s\n  → %s\n", m.To, m.From)
			continue
		}
		if !exists(filepath.Join(dir, m.To)) {
			fmt.Fprintf(os.Stderr, "skip: %s is gone (moved or deleted since)\n", m.To)
			skipped++
			continue
		}
		if err := moveNoClobber(filepath.Join(dir, m.To), filepath.Join(dir, m.From)); err != nil {
			fmt.Fprintln(os.Stderr, "skip:", err)
			skipped++
			continue
		}
		if e, ok := idx.Entries[m.Hash]; ok {
			e.Path = m.From
		}
		back++
		removeEmptyDirs(dir, filepath.Dir(m.To))
	}
	if *dryRun {
		return nil
	}
	run.Undone = true
	if err := log.Save(); err != nil {
		return err
	}
	if err := idx.Save(); err != nil {
		return err
	}
	fmt.Printf("moved %d files back", back)
	if skipped > 0 {
		fmt.Printf(", %d skipped (see above)", skipped)
	}
	fmt.Println()
	return nil
}

// removeEmptyDirs tidies sorted/<category>/ folders that undo emptied. It only
// ever removes empty directories, and never anything outside sorted/.
func removeEmptyDirs(dir, rel string) {
	for rel != "." && (rel == sortedDir || strings.HasPrefix(rel, sortedDir+string(filepath.Separator))) {
		if os.Remove(filepath.Join(dir, rel)) != nil {
			return // not empty
		}
		rel = filepath.Dir(rel)
	}
}

func parseDate(s string) time.Time {
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return time.Now()
	}
	return t
}
