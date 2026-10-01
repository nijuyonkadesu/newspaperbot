package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"sync"
	"testing"

	"tgblogbot/internal/post"
)

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
