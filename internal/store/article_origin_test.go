package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"newspaperbot/internal/post"
)

func originArticle(t *testing.T, slug string) post.Article {
	t.Helper()
	d := post.Draft{Title: slug, Summary: "Summary", Content: "Body", Category: "concept", Slug: slug, PublishedAt: time.Date(2020, 1, 2, 0, 0, 0, 0, time.UTC)}
	raw, err := d.PortfolioMarkdown()
	if err != nil {
		t.Fatal(err)
	}
	article, err := post.ReadArticle("content/tweets/269-"+slug+".md", string(raw))
	if err != nil {
		t.Fatal(err)
	}
	return article
}

func TestRemovedOriginArticleRetainsHistoryWithoutStaleJobsOrDrafts(t *testing.T) {
	ctx := context.Background()
	s, err := Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	d, err := s.StartRevision(ctx, originArticle(t, "original"))
	if err != nil {
		t.Fatal(err)
	}
	d.Content, d.CardID = "Uncommitted edits", 501
	if err := s.Save(ctx, &d); err != nil {
		t.Fatal(err)
	}
	d, err = s.Queue(ctx, d.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	jobs, err := s.PendingPublications(ctx)
	if err != nil || len(jobs) != 1 {
		t.Fatal("missing pending edit", err)
	}
	draft, err := s.New(ctx, []string{"concept"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SyncArticles(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetArticle(ctx, 269); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("deleted origin article remained available", err)
	}
	if _, err := s.FromMessage(ctx, 501); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("old card remained actionable", err)
	}
	if _, err := s.Queue(ctx, d.ID, 0); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("old article edit could be requeued", err)
	}
	if err := s.SavePublication(ctx, &jobs[0]); !errors.Is(err, ErrPublicationLocked) {
		t.Fatal("stale checkpoint revived a retired job", err)
	}
	if pending, err := s.PendingPublications(ctx); err != nil || len(pending) != 0 {
		t.Fatal("removed article retained pending jobs", err)
	}
	if revisions, err := s.Revisions(ctx); err != nil || len(revisions) != 0 {
		t.Fatal("removed article appeared as an active edit", err)
	}
	if active, err := s.Active(ctx); err != nil || active.ID != draft.ID || active.Slot != 1 {
		t.Fatal("origin synchronization changed an unrelated draft", err)
	}
	var raw []byte
	var number sql.NullInt64
	if err := s.db.QueryRow("SELECT data,number FROM drafts WHERE id=?", d.ID).Scan(&raw, &number); err != nil {
		t.Fatal(err)
	}
	var history post.Draft
	if err := json.Unmarshal(raw, &history); err != nil || !history.Archived || history.Content != "Uncommitted edits" || history.Number != 269 || number.Valid {
		t.Fatal("retirement deleted history or kept the number reserved", err)
	}
}

func TestRevisionWithReusedNumberGetsFreshDeliveryIdentity(t *testing.T) {
	ctx := context.Background()
	s, err := Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	original, err := s.StartRevision(ctx, originArticle(t, "original"))
	if err != nil {
		t.Fatal(err)
	}
	original.ChannelID, original.ContentMessageID, original.Delivery = -1001, 501, "sent"
	if err := s.Save(ctx, &original); err != nil {
		t.Fatal(err)
	}
	replacement, err := s.StartRevision(ctx, originArticle(t, "replacement"))
	if err != nil || replacement.ID == original.ID || replacement.Title != "replacement" || replacement.ChannelID != 0 || replacement.ContentMessageID != 0 {
		t.Fatalf("reused number inherited the old pending edit: %+v, %v", replacement, err)
	}
}
