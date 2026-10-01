package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"newspaperbot/internal/post"
)

func TestDraftSlotsReuseGapsAndResetWhileArticleNumbersStayIndependent(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, "drafts.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	drafts := make([]post.Draft, 3)
	for i := range drafts {
		drafts[i], err = s.New(ctx, []string{"development"}, nil)
		if err != nil || drafts[i].Slot != int64(i+1) {
			t.Fatalf("draft %d got slot %d: %v", i, drafts[i].Slot, err)
		}
	}
	deletedID := drafts[1].ID
	if _, err := s.Delete(ctx, deletedID); err != nil {
		t.Fatal(err)
	}
	replacement, err := s.New(ctx, []string{"development"}, nil)
	if err != nil || replacement.Slot != 2 || replacement.ID == deletedID {
		t.Fatalf("gap was not reused independently of the row ID: %+v, %v", replacement, err)
	}

	published := drafts[2]
	published.Title, published.Summary, published.Content = "Title", "Summary", "Body"
	if err := s.Save(ctx, &published); err != nil {
		t.Fatal(err)
	}
	published, err = s.Reserve(ctx, published.ID, 40, 0, dir)
	if err != nil {
		t.Fatal(err)
	}
	if published.Slot != 0 || published.Number != 41 || published.PublishedAt.IsZero() {
		t.Fatalf("publish mixed draft slot and article identity: %+v", published)
	}
	next, err := s.New(ctx, []string{"development"}, nil)
	if err != nil || next.Slot != 3 {
		t.Fatalf("published slot was not released: %+v, %v", next, err)
	}

	for _, d := range []post.Draft{drafts[0], replacement, next} {
		if _, err := s.Delete(ctx, d.ID); err != nil {
			t.Fatal(err)
		}
	}
	reset, err := s.New(ctx, []string{"development"}, nil)
	if err != nil || reset.Slot != 1 || reset.ID <= next.ID {
		t.Fatalf("empty draft list did not restart at slot 1: %+v, %v", reset, err)
	}
}

func TestExistingDatabaseReceivesDraftSlots(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "legacy.db")
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE drafts (id INTEGER PRIMARY KEY AUTOINCREMENT, data TEXT NOT NULL, number INTEGER UNIQUE);
		CREATE TABLE settings (key TEXT PRIMARY KEY, value INTEGER NOT NULL);
		CREATE TABLE publications (operation TEXT PRIMARY KEY, draft_id INTEGER NOT NULL UNIQUE, state TEXT NOT NULL, data TEXT NOT NULL);`); err != nil {
		t.Fatal(err)
	}
	for _, draft := range []post.Draft{
		{Title: "First", UpdatedAt: time.Now().UTC()},
		{Title: "Published", Number: 77, UpdatedAt: time.Now().UTC()},
		{Title: "Second", UpdatedAt: time.Now().UTC()},
	} {
		data, err := json.Marshal(draft)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec("INSERT INTO drafts(data,number) VALUES (?,NULLIF(?,0))", data, draft.Number); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	drafts, err := s.List(ctx)
	if err != nil || len(drafts) != 2 || drafts[0].Slot != 1 || drafts[1].Slot != 2 || drafts[0].Title != "First" || drafts[1].Title != "Second" {
		t.Fatalf("legacy drafts were not assigned compact slots: %+v, %v", drafts, err)
	}
	published, err := s.Get(ctx, 2)
	if err != nil || published.Slot != 0 || published.Number != 77 {
		t.Fatalf("published legacy row received a draft slot: %+v, %v", published, err)
	}
}

func TestRestartPauseAndMultipleDrafts(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "drafts.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	d, err := s.New(ctx, []string{"development"}, []string{"go"})
	if err != nil {
		t.Fatal(err)
	}
	d.Step, d.PendingContent, d.LastMessageID = "content", "## Unfinished\n", 99
	if err := s.Save(ctx, &d); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	got, err := s.Active(ctx)
	if err != nil || got.ID != d.ID || got.Step != "content" || got.PendingContent != d.PendingContent || got.LastMessageID != 99 {
		t.Fatalf("lost state: %+v, %v", got, err)
	}
	if err := s.SetSetting(ctx, "active", 0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Active(ctx); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("pause did not clear active draft")
	}
	if _, err := s.New(ctx, []string{"development"}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(ctx, d.ID); err != nil {
		t.Fatal("starting another draft lost the first")
	}
	if err := s.SetSetting(ctx, "active", d.ID); err != nil {
		t.Fatal(err)
	}
	if got, err := s.Active(ctx); err != nil || got.PendingContent != d.PendingContent {
		t.Fatal("resume lost content")
	}
}

func TestConcurrentReservationsAndFrozenDestination(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, "drafts.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var ids []int64
	for range 8 {
		d, err := s.New(ctx, []string{"development"}, nil)
		if err != nil {
			t.Fatal(err)
		}
		d.Title, d.Summary, d.Content, d.Category, d.Step = "Title", "Summary", "Body", "development", post.Review
		if err := s.Save(ctx, &d); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, d.ID)
	}
	var wg sync.WaitGroup
	results := make(chan post.Draft, len(ids))
	for _, id := range ids {
		wg.Add(1)
		go func() {
			defer wg.Done()
			d, err := s.Reserve(ctx, id, 42, -1001, dir)
			if err != nil {
				t.Error(err)
				return
			}
			results <- d
		}()
	}
	wg.Wait()
	close(results)
	numbers := map[int64]bool{}
	for d := range results {
		if numbers[d.Number] || d.Number < 43 || d.Number > 50 {
			t.Fatalf("invalid number %d", d.Number)
		}
		numbers[d.Number] = true
		again, err := s.Reserve(ctx, d.ID, 100, -2002, dir)
		if err != nil || again.Number != d.Number || again.ChannelID != -1001 || again.Filename != d.Filename {
			t.Fatal("repeated reservation changed the publication")
		}
		if err := d.Replace(post.Source{Text: "Title\n\nSummary\n\nNew body"}); err == nil {
			t.Fatal("reserved post remained editable")
		}
	}
	if len(numbers) != len(ids) {
		t.Fatal("missing reservations")
	}
}
