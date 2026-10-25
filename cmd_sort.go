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

// lastActive returns the newest sort that hasn't been undone, or nil.
func (l *MoveLog) lastActive() *SortRun {
	for _, r := range slices.Backward(l.Runs) {
		if !r.Undone {
			return r
		}
	}
	return nil
}

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

// SortPlan lists the moves a sort would make.
type SortPlan struct {
	Moves   []Move `json:"moves"`
	Already int    `json:"already"`
	Missing int    `json:"missing"`
}

func planSort(dir string, idx *Index) SortPlan {
	p := SortPlan{Moves: []Move{}}
	claimed := map[string]bool{} // targets taken earlier in this plan
	taken := func(rel string) bool { return claimed[rel] || exists(filepath.Join(dir, rel)) }
	for _, e := range idx.Sorted() {
		if !exists(filepath.Join(dir, e.Path)) {
			p.Missing++
			continue
		}
		// Already filed in the right category folder: leave it alone, so
		// sorting twice doesn't rename anything.
		if filepath.Dir(e.Path) == filepath.Join(sortedDir, e.Category) {
			p.Already++
			continue
		}
		want := filepath.Join(sortedDir, e.Category, SortedName(parseDate(e.Date), e.Title, filepath.Ext(e.Path)))
		to := UniquePath(want, taken)
		claimed[to] = true
		p.Moves = append(p.Moves, Move{e.Hash, e.Path, to})
	}
	return p
}

// applySort performs planned moves, logging each one before updating the
// index: if we crash in between, undo still knows where the file went.
func applySort(dir string, idx *Index, moves []Move, emit func(Move, error)) (int, error) {
	log, err := loadMoveLog(dir)
	if err != nil {
		return 0, err
	}
	run := &SortRun{At: time.Now()}
	for _, m := range moves {
		e, ok := idx.Entries[m.Hash]
		if !ok || e.Path != m.From {
			emit(m, errors.New("index changed since the preview; skipped"))
			continue
		}
		if err := moveNoClobber(filepath.Join(dir, m.From), filepath.Join(dir, m.To)); err != nil {
			emit(m, err)
			continue
		}
		if len(run.Moves) == 0 {
			log.Runs = append(log.Runs, run)
		}
		run.Moves = append(run.Moves, m)
		e.Path = m.To
		if err := log.Save(); err != nil {
			return len(run.Moves), err
		}
		if err := idx.Save(); err != nil {
			return len(run.Moves), err
		}
		emit(m, nil)
	}
	return len(run.Moves), nil
}

// undoLast reverses the newest sort, never overwriting anything.
func undoLast(dir string, emit func(Move, error)) (run *SortRun, back int, err error) {
	log, err := loadMoveLog(dir)
	if err != nil {
		return nil, 0, err
	}
	if run = log.lastActive(); run == nil {
		return nil, 0, errors.New("no sort to undo")
	}
	idx, err := LoadIndex(dir)
	if err != nil {
		return nil, 0, err
	}
	for _, m := range slices.Backward(run.Moves) {
		if !exists(filepath.Join(dir, m.To)) {
			emit(m, fmt.Errorf("%s is gone (moved or deleted since)", m.To))
			continue
		}
		if err := moveNoClobber(filepath.Join(dir, m.To), filepath.Join(dir, m.From)); err != nil {
			emit(m, err)
			continue
		}
		if e, ok := idx.Entries[m.Hash]; ok {
			e.Path = m.From
		}
		back++
		removeEmptyDirs(dir, filepath.Dir(m.To))
		emit(m, nil)
	}
	run.Undone = true
	if err := log.Save(); err != nil {
		return run, back, err
	}
	return run, back, idx.Save()
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

	plan := planSort(dir, idx)
	moved := len(plan.Moves)
	if *dryRun {
		for _, m := range plan.Moves {
			fmt.Printf("%s\n  → %s\n", m.From, m.To)
		}
	} else {
		moved, err = applySort(dir, idx, plan.Moves, func(m Move, err error) {
			if err != nil {
				fmt.Fprintln(os.Stderr, "skip:", err)
				return
			}
			fmt.Printf("%s → %s\n", m.From, m.To)
		})
		if err != nil {
			return err
		}
	}

	verb := "moved"
	if *dryRun {
		verb = "would move"
	}
	fmt.Printf("\n%s %d files", verb, moved)
	if plan.Already > 0 {
		fmt.Printf(", %d already sorted", plan.Already)
	}
	if plan.Missing > 0 {
		fmt.Printf(", %d indexed files not found on disk", plan.Missing)
	}
	fmt.Println()
	if !*dryRun && moved > 0 {
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

	if *dryRun {
		log, err := loadMoveLog(dir)
		if err != nil {
			return err
		}
		run := log.lastActive()
		if run == nil {
			return errors.New("no sort to undo")
		}
		fmt.Printf("would undo sort from %s (%d moves)\n", run.At.Format("Mon 2 Jan 15:04"), len(run.Moves))
		for _, m := range slices.Backward(run.Moves) {
			fmt.Printf("%s\n  → %s\n", m.To, m.From)
		}
		return nil
	}

	skipped := 0
	run, back, err := undoLast(dir, func(m Move, err error) {
		if err != nil {
			fmt.Fprintln(os.Stderr, "skip:", err)
			skipped++
		}
	})
	if err != nil {
		return err
	}
	fmt.Printf("undid sort from %s: moved %d files back", run.At.Format("Mon 2 Jan 15:04"), back)
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
