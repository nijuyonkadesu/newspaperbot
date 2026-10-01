package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"newspaperbot/internal/post"
)

func TestPublicationFreezesContentAndSurvivesRestart(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "drafts.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	d, err := s.New(ctx, []string{"concept"}, []string{"go"})
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Replace(post.Source{MessageID: 5, Text: "Title\n\nSummary\n\nBody\nCategory: new-category\nTags: new-tag"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Save(ctx, &d); err != nil {
		t.Fatal(err)
	}
	stale := d
	d, err = s.Queue(ctx, d.ID, -1001)
	if err != nil {
		t.Fatal(err)
	}
	if !d.Locked() || d.GitState != "queued" || d.ChannelID != -1001 || d.PublishedAt.IsZero() {
		t.Fatal("publication not frozen")
	}
	if err := s.Save(ctx, &stale); err == nil {
		t.Fatal("stale card overwrote queued publication")
	}
	if _, err := s.Delete(ctx, d.ID); !errors.Is(err, ErrPublicationLocked) {
		t.Fatal("queued post was deleted")
	}
	if err := d.Append(post.Source{Text: "Addition"}); err == nil {
		t.Fatal("queued post editable")
	}
	op := d.GitOperation
	if _, err := s.Queue(ctx, d.ID, -2002); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	jobs, err := s.PendingPublications(ctx)
	if err != nil || len(jobs) != 1 {
		t.Fatalf("pending jobs %v %v", jobs, err)
	}
	job := &jobs[0]
	if job.Operation != op || job.Draft.ChannelID != -1001 || job.Draft.Content != "Body" || job.Draft.Category != "new-category" {
		t.Fatal("frozen job changed")
	}
	d, err = s.Get(ctx, d.ID)
	if err != nil {
		t.Fatal(err)
	}
	d.CardID, d.Preview, d.View = 77, true, "preview"
	if err := s.Save(ctx, &d); err != nil {
		t.Fatal(err)
	}
	job.CommitSHA, job.Number, job.Filename, job.Slug, job.State = "abcdef", 269, "content/tweets/269-title.md", "title", "pushed"
	if err := s.SavePublication(ctx, job); err != nil {
		t.Fatal(err)
	}
	d, err = s.FinishPublication(ctx, job)
	if err != nil {
		t.Fatal(err)
	}
	if d.CardID != 77 || !d.Preview || d.CommitSHA != "abcdef" || d.Number != 269 || d.GitState != "done" || !d.Exported {
		t.Fatalf("completion lost state: %+v", d)
	}
	if jobs, err := s.PendingPublications(ctx); err != nil || len(jobs) != 0 {
		t.Fatal("completed job remained pending")
	}
}

func TestFailedPublicationRetriesSameOperation(t *testing.T) {
	ctx := context.Background()
	s, err := Open(filepath.Join(t.TempDir(), "drafts.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	d, err := s.New(ctx, []string{"concept"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	d.Title, d.Summary, d.Content = "Title", "Summary", "Body"
	if err := s.Save(ctx, &d); err != nil {
		t.Fatal(err)
	}
	d, err = s.Queue(ctx, d.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	jobs, err := s.PendingPublications(ctx)
	if err != nil || len(jobs) != 1 {
		t.Fatal("missing job")
	}
	job := &jobs[0]
	job.State, job.Error, job.CommitSHA = "failed", "network failure", "abc"
	if err := s.SavePublication(ctx, job); err != nil {
		t.Fatal(err)
	}
	d.GitState = "failed"
	if err := s.Save(ctx, &d); err != nil {
		t.Fatal(err)
	}
	retried, err := s.Queue(ctx, d.ID, 99)
	if err != nil {
		t.Fatal(err)
	}
	jobs, err = s.PendingPublications(ctx)
	if err != nil || len(jobs) != 1 || retried.GitOperation != job.Operation || jobs[0].CommitSHA != "abc" || jobs[0].State != "queued" || retried.ChannelID != 0 {
		t.Fatal("retry changed operation or frozen destination")
	}
}
