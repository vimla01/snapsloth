package main

import "testing"

func TestReview(t *testing.T) {
	dir := t.TempDir()
	idx := &Index{Version: 1, Entries: map[string]*Entry{}, path: indexPath(dir)}
	if err := idx.Put(&Entry{Hash: "h1", Category: "meme"}); err != nil {
		t.Fatal(err)
	}

	// Confirming keeps the model's answer as the only one.
	e, err := idx.Review("h1", "meme")
	if err != nil || !e.Reviewed || e.ModelCategory != "" || e.ModelSaid() != "meme" {
		t.Fatalf("confirm: %+v, %v", e, err)
	}
	// Fixing remembers what the model said.
	e, _ = idx.Review("h1", "chat")
	if e.Category != "chat" || e.ModelSaid() != "meme" {
		t.Fatalf("fix: %+v", e)
	}
	// Fixing again keeps the original model answer, not the first fix.
	e, _ = idx.Review("h1", "social_post")
	if e.ModelSaid() != "meme" {
		t.Fatalf("second fix: %+v", e)
	}
	// Changing back to the model's answer clears the override.
	e, _ = idx.Review("h1", "meme")
	if e.ModelCategory != "" || e.Category != "meme" {
		t.Fatalf("revert: %+v", e)
	}
	if _, err := idx.Review("nope", "meme"); err == nil {
		t.Fatal("unknown hash should fail")
	}

	// It all survives a reload.
	idx.Review("h1", "chat")
	loaded, err := LoadIndex(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := loaded.Entries["h1"]; got.Category != "chat" || got.ModelSaid() != "meme" || !got.Reviewed {
		t.Fatalf("after reload: %+v", got)
	}
}
