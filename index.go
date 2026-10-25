package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
)

const stateDir = ".snapsloth"

var supportedExts = []string{".png", ".jpg", ".jpeg", ".webp"}

// Entry is everything snapsloth knows about one screenshot. Text, Title and
// Summary are stored already masked, so the index is not a leak itself.
type Entry struct {
	Hash           string    `json:"hash"`
	Path           string    `json:"path"` // relative to the screenshots folder; updated by sort/undo
	OrigName       string    `json:"orig_name"`
	Date           string    `json:"date"` // YYYY-MM-DD, from the file name or mtime
	Category       string    `json:"category"`
	Title          string    `json:"title"`
	Summary        string    `json:"summary"`
	Text           string    `json:"text"`
	Todo           string    `json:"todo,omitempty"` // what's still to do, per the model
	Sensitive      bool      `json:"sensitive"`
	SensitiveTypes []string  `json:"sensitive_types"`
	Findings       []Finding `json:"findings,omitempty"` // regex hits, masked
	Model          string    `json:"model"`
	Seconds        float64   `json:"seconds"`
	ScannedAt      time.Time `json:"scanned_at"`

	// Set when the user checks or fixes the category in the UI. These
	// double as labels for `eval`.
	Reviewed      bool   `json:"reviewed,omitempty"`
	ModelCategory string `json:"model_category,omitempty"` // the model's answer, if the user changed it
}

type Index struct {
	Version int               `json:"version"`
	Entries map[string]*Entry `json:"entries"` // by SHA-256 of the file
	path    string
	mu      sync.Mutex // the UI edits entries while a scan is saving
}

func indexPath(dir string) string { return filepath.Join(dir, stateDir, "index.json") }

// LoadIndex reads the folder's index, or returns an empty one if there is none yet.
func LoadIndex(dir string) (*Index, error) {
	idx := &Index{Version: 1, Entries: map[string]*Entry{}, path: indexPath(dir)}
	data, err := os.ReadFile(idx.path)
	if errors.Is(err, fs.ErrNotExist) {
		return idx, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, idx); err != nil {
		return nil, errors.New("corrupt index " + idx.path + ": " + err.Error())
	}
	if idx.Entries == nil {
		idx.Entries = map[string]*Entry{}
	}
	return idx, nil
}

// Save writes the index atomically, so a crash mid-write never corrupts it.
func (idx *Index) Save() error {
	idx.mu.Lock()
	defer idx.mu.Unlock()
	return writeJSONAtomic(idx.path, idx)
}

// Put adds or replaces an entry and saves.
func (idx *Index) Put(e *Entry) error {
	idx.mu.Lock()
	idx.Entries[e.Hash] = e
	idx.mu.Unlock()
	return idx.Save()
}

// Review records the user's category for an entry and saves.
func (idx *Index) Review(hash, category string) (*Entry, error) {
	idx.mu.Lock()
	e, ok := idx.Entries[hash]
	if ok {
		if category != e.Category && e.ModelCategory == "" {
			e.ModelCategory = e.Category
		}
		if category == e.ModelCategory {
			e.ModelCategory = "" // changed back to what the model said
		}
		e.Category, e.Reviewed = category, true
	}
	idx.mu.Unlock()
	if !ok {
		return nil, errors.New("not in the index")
	}
	return e, idx.Save()
}

// ModelSaid is the category the model gave, even if the user changed it since.
func (e *Entry) ModelSaid() string {
	if e.ModelCategory != "" {
		return e.ModelCategory
	}
	return e.Category
}

// Sorted returns entries ordered by date, then path, for stable output.
func (idx *Index) Sorted() []*Entry {
	out := make([]*Entry, 0, len(idx.Entries))
	for _, e := range idx.Entries {
		out = append(out, e)
	}
	slices.SortFunc(out, func(a, b *Entry) int {
		if c := strings.Compare(a.Date, b.Date); c != 0 {
			return c
		}
		return strings.Compare(a.Path, b.Path)
	})
	return out
}

func writeJSONAtomic(path string, v any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// FindImages lists supported images under dir (relative paths), skipping
// snapsloth's own state folder.
func FindImages(dir string) ([]string, error) {
	var out []string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == stateDir {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Type().IsRegular() && slices.Contains(supportedExts, strings.ToLower(filepath.Ext(path))) {
			rel, err := filepath.Rel(dir, path)
			if err != nil {
				return err
			}
			out = append(out, rel)
		}
		return nil
	})
	slices.Sort(out)
	return out, err
}

func hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// captureDate prefers the date in the file name; screenshots copied between
// machines often lose their mtime.
func captureDate(path string) string {
	if t, ok := DateFromName(path); ok {
		return t.Format("2006-01-02")
	}
	if info, err := os.Stat(path); err == nil {
		return info.ModTime().Format("2006-01-02")
	}
	return time.Now().Format("2006-01-02")
}
