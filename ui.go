package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"
)

//go:embed ui.html
var uiHTML []byte

// uiJob is the state of the scan running in the background, if any.
type uiJob struct {
	Running   bool      `json:"running"`
	I         int       `json:"i"`
	N         int       `json:"n"`
	Current   string    `json:"current"`
	Done      int       `json:"done"`
	Failed    int       `json:"failed"`
	Seconds   float64   `json:"seconds"` // model time so far
	Recent    []*Entry  `json:"recent"`  // newest first, last few
	Errors    []string  `json:"errors"`
	Error     string    `json:"error"`
	StartedAt time.Time `json:"started_at"`
	cancel    context.CancelFunc
}

type uiServer struct {
	mu          sync.Mutex
	dir         string
	model, host string
	job         *uiJob
	allowedHost map[string]bool

	ollamaAt  time.Time // cached Ollama check, so polling doesn't hammer it
	ollamaErr string
}

func cmdUI(args []string) error {
	fs := flag.NewFlagSet("ui", flag.ExitOnError)
	model := fs.String("model", "gemma4:e4b", "Ollama vision model")
	host := fs.String("host", defaultHost(), "Ollama URL")
	port := fs.Int("port", 8765, "port for the UI (localhost only)")
	noOpen := fs.Bool("no-open", false, "don't open the browser")
	pos, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	s := &uiServer{model: *model, host: *host}
	switch {
	case len(pos) == 1:
		if s.dir, err = dirArg(pos, 1, "ui [dir]"); err != nil {
			return err
		}
	case len(pos) == 0:
		if home, err := os.UserHomeDir(); err == nil {
			if d := filepath.Join(home, "Pictures", "Screenshots"); exists(d) {
				s.dir = d
			}
		}
	default:
		return errors.New("usage: snapsloth ui [dir] [--port 8765]")
	}

	ln, err := listenLocal(*port)
	if err != nil {
		return err
	}
	addr := ln.Addr().(*net.TCPAddr)
	s.allowedHost = map[string]bool{
		fmt.Sprintf("127.0.0.1:%d", addr.Port): true,
		fmt.Sprintf("localhost:%d", addr.Port): true,
	}
	url := fmt.Sprintf("http://127.0.0.1:%d", addr.Port)
	fmt.Printf("🦥 SnapSloth UI on %s (only reachable from this computer)\nCtrl+C to quit.\n", url)
	if !*noOpen {
		openInOS(url)
	}
	return http.Serve(ln, s.routes())
}

// listenLocal binds to loopback only, trying a few ports if the first is busy.
func listenLocal(port int) (net.Listener, error) {
	var err error
	for p := port; p < port+10; p++ {
		var ln net.Listener
		if ln, err = net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", p)); err == nil {
			return ln, nil
		}
	}
	return nil, err
}

func openInOS(target string) {
	cmd := "xdg-open"
	if runtime.GOOS == "darwin" {
		cmd = "open"
	}
	_ = exec.Command(cmd, target).Start()
}

func (s *uiServer) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(uiHTML)
	})
	mux.HandleFunc("GET /api/status", s.handleStatus)
	mux.HandleFunc("GET /api/entries", s.handleEntries)
	mux.HandleFunc("GET /api/img/{hash}", s.handleImage)
	mux.HandleFunc("POST /api/folder", s.handleFolder)
	mux.HandleFunc("POST /api/scan", s.handleScan)
	mux.HandleFunc("POST /api/scan/stop", s.handleStop)
	mux.HandleFunc("POST /api/sort/preview", s.handleSortPreview)
	mux.HandleFunc("POST /api/sort", s.handleSort)
	mux.HandleFunc("POST /api/undo", s.handleUndo)
	mux.HandleFunc("POST /api/reveal/{hash}", s.handleReveal)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Only our own page may talk to us: a wrong Host means DNS rebinding,
		// and the custom header can't be sent cross-site without CORS.
		if !s.allowedHost[r.Host] {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		if r.Method == http.MethodPost && r.Header.Get("X-SnapSloth") != "1" {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		mux.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
}

func (s *uiServer) currentDir() (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.dir == "" {
		return "", errors.New("pick your screenshots folder first")
	}
	return s.dir, nil
}

func (s *uiServer) busy() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.job != nil && s.job.Running
}

func (s *uiServer) checkOllama() string {
	s.mu.Lock()
	if time.Since(s.ollamaAt) < 5*time.Second {
		defer s.mu.Unlock()
		return s.ollamaErr
	}
	host, model := s.host, s.model
	s.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	msg := ""
	if err := NewOllamaClient(host, model).Check(ctx); err != nil {
		msg = err.Error()
	}
	s.mu.Lock()
	s.ollamaAt, s.ollamaErr = time.Now(), msg
	s.mu.Unlock()
	return msg
}

func (s *uiServer) handleStatus(w http.ResponseWriter, r *http.Request) {
	out := map[string]any{"model": s.model, "host": s.host, "ollama_error": s.checkOllama()}
	if home, err := os.UserHomeDir(); err == nil {
		out["home"] = home
	}
	s.mu.Lock()
	out["dir"] = s.dir
	if s.job != nil {
		j := *s.job
		out["job"] = &j
	}
	dir := s.dir
	s.mu.Unlock()

	if dir != "" {
		idx, err := LoadIndex(dir)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		// A cheap estimate of new files (no hashing): images whose path
		// isn't in the index. Scan does the exact check.
		known := map[string]bool{}
		for _, e := range idx.Entries {
			known[e.Path] = true
		}
		files, _ := FindImages(dir)
		pending := 0
		for _, f := range files {
			if !known[f] {
				pending++
			}
		}
		log, _ := loadMoveLog(dir)
		out["files"] = len(files)
		out["pending"] = pending
		out["report"] = buildReport(idx, time.Now())
		out["can_undo"] = log != nil && log.lastActive() != nil
	}
	writeJSON(w, out)
}

type entryView struct {
	*Entry
	Abs    string   `json:"abs"`
	Reason string   `json:"reason,omitempty"`
	Tips   []string `json:"tips,omitempty"`
}

func (s *uiServer) handleEntries(w http.ResponseWriter, r *http.Request) {
	dir, err := s.currentDir()
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	idx, err := LoadIndex(dir)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	q := r.URL.Query()
	entries := idx.Sorted()
	slices.Reverse(entries) // newest first
	if terms := strings.Fields(strings.ToLower(q.Get("q"))); len(terms) > 0 {
		hits := rank(idx.Sorted(), terms, true)
		if len(hits) == 0 {
			hits = rank(idx.Sorted(), terms, false)
		}
		entries = entries[:0]
		for _, h := range hits {
			entries = append(entries, h.e)
		}
	}
	cat, riskyOnly := q.Get("cat"), q.Get("risky") == "1"

	out := []entryView{}
	for _, e := range entries {
		if (cat != "" && e.Category != cat) || (riskyOnly && !e.Sensitive) {
			continue
		}
		v := entryView{Entry: e, Abs: filepath.Join(dir, e.Path)}
		if e.Sensitive {
			v.Reason = riskReason(e)
			for _, t := range e.SensitiveTypes {
				if a, ok := riskAdvice[t]; ok && !slices.Contains(v.Tips, a) {
					v.Tips = append(v.Tips, a)
				}
			}
		}
		out = append(out, v)
	}
	writeJSON(w, out)
}

// entryByHash finds an indexed file by hash, so the browser never sends
// paths and can't ask for anything outside the index.
func (s *uiServer) entryByHash(hash string) (string, *Entry, error) {
	dir, err := s.currentDir()
	if err != nil {
		return "", nil, err
	}
	idx, err := LoadIndex(dir)
	if err != nil {
		return "", nil, err
	}
	e, ok := idx.Entries[hash]
	if !ok {
		return "", nil, errors.New("not in the index")
	}
	return dir, e, nil
}

func (s *uiServer) handleImage(w http.ResponseWriter, r *http.Request) {
	dir, e, err := s.entryByHash(r.PathValue("hash"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	f, err := os.Open(filepath.Join(dir, e.Path))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Cache-Control", "private, max-age=3600")
	http.ServeContent(w, r, e.Path, info.ModTime(), f)
}

func (s *uiServer) handleReveal(w http.ResponseWriter, r *http.Request) {
	dir, e, err := s.entryByHash(r.PathValue("hash"))
	if err != nil {
		writeErr(w, http.StatusNotFound, err)
		return
	}
	openInOS(filepath.Dir(filepath.Join(dir, e.Path)))
	writeJSON(w, map[string]bool{"ok": true})
}

func (s *uiServer) handleFolder(w http.ResponseWriter, r *http.Request) {
	var req struct{ Dir string }
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if s.busy() {
		writeErr(w, http.StatusConflict, errors.New("wait for the scan to finish (or stop it) first"))
		return
	}
	d := strings.TrimSpace(req.Dir)
	if d == "~" || strings.HasPrefix(d, "~/") {
		home, _ := os.UserHomeDir()
		d = filepath.Join(home, strings.TrimPrefix(d, "~"))
	}
	dir, err := dirArg([]string{d}, 1, "")
	if err != nil {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("can't open that folder: %v", err))
		return
	}
	s.mu.Lock()
	s.dir, s.job = dir, nil
	s.mu.Unlock()
	writeJSON(w, map[string]string{"dir": dir})
}

func (s *uiServer) handleScan(w http.ResponseWriter, r *http.Request) {
	var req struct{ Limit int }
	_ = json.NewDecoder(r.Body).Decode(&req)
	dir, err := s.currentDir()
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if s.busy() {
		writeErr(w, http.StatusConflict, errors.New("a scan is already running"))
		return
	}
	client := NewOllamaClient(s.host, s.model)
	if err := client.Check(r.Context()); err != nil {
		writeErr(w, http.StatusServiceUnavailable, err)
		return
	}
	idx, err := LoadIndex(dir)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	plan, err := planScan(dir, idx)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	todo := plan.Todo
	if req.Limit > 0 && len(todo) > req.Limit {
		todo = todo[:req.Limit]
	}
	if len(todo) == 0 {
		idx.Save()
		writeJSON(w, map[string]any{"started": false, "message": "Nothing new to scan. All caught up!"})
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	job := &uiJob{Running: true, N: len(todo), StartedAt: time.Now(), cancel: cancel, Recent: []*Entry{}, Errors: []string{}}
	job.Current = todo[0].rel
	s.mu.Lock()
	s.job = job
	s.mu.Unlock()

	go func() {
		err := runScan(ctx, dir, idx, client, todo, func(ev ScanEvent) {
			s.mu.Lock()
			defer s.mu.Unlock()
			job.I = ev.I
			if ev.I < len(todo) {
				job.Current = todo[ev.I].rel
			}
			if ev.Err != nil {
				job.Failed++
				job.Errors = append(job.Errors, ev.Rel+": "+ev.Err.Error())
				return
			}
			job.Done++
			job.Seconds += ev.Seconds
			job.Recent = append([]*Entry{ev.Entry}, job.Recent[:min(5, len(job.Recent))]...)
		})
		s.mu.Lock()
		defer s.mu.Unlock()
		job.Running, job.Current = false, ""
		if err != nil && !errors.Is(err, context.Canceled) {
			job.Error = err.Error()
		}
	}()
	writeJSON(w, map[string]any{"started": true, "n": len(todo)})
}

func (s *uiServer) handleStop(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	if s.job != nil && s.job.cancel != nil {
		s.job.cancel()
	}
	s.mu.Unlock()
	writeJSON(w, map[string]bool{"ok": true})
}

func (s *uiServer) handleSortPreview(w http.ResponseWriter, r *http.Request) {
	dir, err := s.currentDir()
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	idx, err := LoadIndex(dir)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, planSort(dir, idx))
}

// handleSort recomputes the plan rather than trusting paths from the
// browser; it matches the preview unless something changed in between.
func (s *uiServer) handleSort(w http.ResponseWriter, r *http.Request) {
	dir, err := s.currentDir()
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if s.busy() {
		writeErr(w, http.StatusConflict, errors.New("wait for the scan to finish first"))
		return
	}
	idx, err := LoadIndex(dir)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	plan := planSort(dir, idx)
	skipped := []string{}
	moved, err := applySort(dir, idx, plan.Moves, func(m Move, err error) {
		if err != nil {
			skipped = append(skipped, m.From+": "+err.Error())
		}
	})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, map[string]any{"moved": moved, "skipped": skipped})
}

func (s *uiServer) handleUndo(w http.ResponseWriter, r *http.Request) {
	dir, err := s.currentDir()
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if s.busy() {
		writeErr(w, http.StatusConflict, errors.New("wait for the scan to finish first"))
		return
	}
	skipped := []string{}
	_, back, err := undoLast(dir, func(m Move, err error) {
		if err != nil {
			skipped = append(skipped, err.Error())
		}
	})
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, map[string]any{"moved": back, "skipped": skipped})
}
