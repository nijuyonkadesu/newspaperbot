package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"newspaperbot/internal/post"
)

func TestRevisionNeverUsesDraftSlotsAndSurvivesRestart(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "state.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	article := post.Draft{Number: 1000, Filename: "content/tweets/1000-original.md", Slug: "original", Title: "Original", Summary: "Summary", Content: "Body", Category: "concept", Tags: []string{"go"}, PublishedAt: time.Date(2020, 1, 2, 0, 0, 0, 0, time.UTC)}
	raw, err := article.PortfolioMarkdown()
	if err != nil {
		t.Fatal(err)
	}
	live, err := post.ReadArticle(article.Filename, string(raw))
	if err != nil {
		t.Fatal(err)
	}
	d, err := s.StartRevision(ctx, live)
	if err != nil {
		t.Fatal(err)
	}
	d.Title = "Pending title"
	if err := s.Save(ctx, &d); err != nil {
		t.Fatal(err)
	}
	if d.Slot != 0 || d.Locked() {
		t.Fatal("revision was a draft or locked")
	}
	if _, err := s.Delete(ctx, d.ID); !errors.Is(err, ErrPublicationLocked) {
		t.Fatal("draft deletion deleted article")
	}
	draft, err := s.New(ctx, []string{"concept"}, nil)
	if err != nil || draft.Slot != 1 {
		t.Fatal("revision occupied a draft slot")
	}
	if list, err := s.List(ctx); err != nil || len(list) != 1 || list[0].ID != draft.ID {
		t.Fatal("revision appeared in drafts")
	}
	s.Close()
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	d, err = s.StartRevision(ctx, live)
	if err != nil || d.Title != "Pending title" || d.Slot != 0 {
		t.Fatal("restart/reopen discarded pending changes")
	}
	d.ChannelID, d.ContentMessageID, d.Delivery = -1001, 222, "sent"
	if err := s.Save(ctx, &d); err != nil {
		t.Fatal(err)
	}
	d, err = s.Queue(ctx, d.ID, -2002)
	if err != nil || !d.PublishedAt.Equal(article.PublishedAt) || d.ChannelID != -1001 || !d.Locked() {
		t.Fatal("queue changed original date/destination", err)
	}
	jobs, err := s.PendingPublications(ctx)
	if err != nil || len(jobs) != 1 {
		t.Fatal("missing revision job")
	}
	jobs[0].Number, jobs[0].Filename, jobs[0].Slug, jobs[0].CommitSHA = 1000, article.Filename, article.Slug, "sha"
	d, err = s.FinishPublication(ctx, &jobs[0])
	if err != nil || !d.Revision.Applied || d.Number != 1000 || d.Delivery != "pending" {
		t.Fatal("revision completion lost identity", err)
	}
	if _, err := s.DiscardRevision(ctx, d.ID); err == nil {
		t.Fatal("discarded already committed changes")
	}
	d.Revision, d.Delivery = nil, "sent"
	if err := s.Save(ctx, &d); err != nil {
		t.Fatal(err)
	}
	d, err = s.StartRevision(ctx, live)
	if err != nil {
		t.Fatal(err)
	}
	d.Title = "Second edit"
	if err := s.Save(ctx, &d); err != nil {
		t.Fatal(err)
	}
	d, err = s.Queue(ctx, d.ID, 0)
	if err != nil || d.GitOperation == jobs[0].Operation {
		t.Fatal("second revision reused old operation", err)
	}
}

func TestDiscardRevisionRestoresLiveContent(t *testing.T) {
	ctx := context.Background()
	s, err := Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	d := post.Draft{Title: "Original", Summary: "Summary", Content: "Body", Slug: "original", Category: "concept", PublishedAt: time.Date(2020, 1, 2, 0, 0, 0, 0, time.UTC)}
	raw, _ := d.PortfolioMarkdown()
	article, err := post.ReadArticle("content/tweets/001-original.md", string(raw))
	if err != nil {
		t.Fatal(err)
	}
	d, err = s.StartRevision(ctx, article)
	if err != nil {
		t.Fatal(err)
	}
	d.Title = "Discard me"
	if err := s.Save(ctx, &d); err != nil {
		t.Fatal(err)
	}
	d, err = s.DiscardRevision(ctx, d.ID)
	if err != nil || d.Title != "Original" || d.Revision != nil || d.Number != 1 || !d.Locked() {
		t.Fatal("discard altered live article", err)
	}
}
