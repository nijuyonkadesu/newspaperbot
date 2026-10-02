package telegram

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"newspaperbot/internal/metadata"
	"newspaperbot/internal/post"
	"newspaperbot/internal/store"
)

type fakeRepository struct {
	mu       sync.Mutex
	entered  chan struct{}
	release  chan struct{}
	fail     bool
	calls    int
	entries  []post.Article
	conflict bool
}

func (r *fakeRepository) Catalog(context.Context) (metadata.Catalog, error) {
	return metadata.Catalog{Categories: []string{"concept", "personal"}, Tags: []string{"go", "sqlite"}, Groups: map[string][]string{"concept": {"sqlite"}, "personal": {"go"}}, LastNumber: 268, Revision: "remote-sha"}, nil
}
func (r *fakeRepository) Refresh(context.Context) error { return nil }
func (r *fakeRepository) Articles(context.Context) ([]post.Article, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]post.Article(nil), r.entries...), nil
}
func (r *fakeRepository) Publish(ctx context.Context, job *store.Publication, checkpoint func() error) error {
	r.mu.Lock()
	r.calls++
	failed := r.fail
	conflict := r.conflict
	r.mu.Unlock()
	if r.entered != nil {
		select {
		case r.entered <- struct{}{}:
		default:
		}
	}
	if r.release != nil {
		select {
		case <-r.release:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if failed {
		return errors.New("simulated Git failure")
	}
	if conflict {
		return post.ErrArticleChanged
	}
	job.Number, job.Filename, job.Slug, job.CommitSHA, job.State = 269, fmt.Sprintf("content/tweets/269-post-%d.md", job.Draft.ID), "title", "remote-commit", "pushed"
	if job.Draft.Revision != nil {
		job.Number, job.Filename, job.Slug = job.Draft.Number, job.Draft.Filename, job.Draft.Slug
	}
	d := job.Draft
	d.Number, d.Filename, d.Slug = job.Number, job.Filename, job.Slug
	raw, err := d.PortfolioMarkdown()
	if err != nil {
		return err
	}
	article, err := post.ReadArticle(d.Filename, string(raw))
	if err != nil {
		return err
	}
	r.mu.Lock()
	r.entries = []post.Article{article}
	r.mu.Unlock()
	return checkpoint()
}

func TestGitPublishingKeepsOtherDraftsEditable(t *testing.T) {
	h := newHarness(t)
	repo := &fakeRepository{entered: make(chan struct{}, 1), release: make(chan struct{})}
	h.app.Repository = repo
	h.send("/setchannel @channel")
	first := h.ready("Body\nCategory: new-category\nTags: new-tag, go")
	h.click("preview")
	before := h.api.live()
	found := false
	for _, call := range before {
		if call.ResultID == first.CardID && strings.Contains(call.RichMarkdown, "new") && strings.Contains(call.RichMarkdown, `\*`) {
			found = true
		}
	}
	if !found {
		t.Fatal("preview did not mark new labels")
	}
	h.click("publish")
	first = h.active()
	if !first.Locked() || first.Number != 0 || first.GitState != "queued" {
		t.Fatal("publish was not queued")
	}
	if h.api.count("sendRichMessage", true) != 0 {
		t.Fatal("channel sent before Git push")
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); h.app.RunPublisher(ctx, h.bot) }()
	defer func() { cancel(); <-done }()
	select {
	case <-repo.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("publisher did not start")
	}
	h.send("/newpost")
	h.send("Other post\n\nSummary\n\nOther body")
	second := h.active()
	h.click("preview")
	h.send("Addition while Git is running")
	second = h.active()
	if second.ID == first.ID || !second.Preview || second.CardID == first.CardID || !strings.Contains(second.Content, "Addition while Git") {
		t.Fatal("Git work blocked or changed another draft")
	}
	close(repo.release)
	deadline := time.Now().Add(3 * time.Second)
	for {
		completed, err := h.app.Store.Get(context.Background(), first.ID)
		if err != nil {
			t.Fatal(err)
		}
		if completed.GitState == "done" && completed.Delivery == "sent" {
			if completed.Content != "Body" || completed.Category != "new-category" || strings.Join(completed.Tags, ",") != "new-tag,go" || completed.CardID != first.CardID {
				t.Fatal("publication altered frozen content or replaced its card")
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("publication did not finish")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if h.active().ID != second.ID {
		t.Fatal("publication stole the active draft")
	}
	if h.api.count("sendRichMessage", true) != 1 {
		t.Fatal("wrong channel delivery count")
	}
}

func TestGitFailureRetryRetainsOperationAndDestination(t *testing.T) {
	h := newHarness(t)
	repo := &fakeRepository{fail: true}
	h.app.Repository = repo
	first := h.ready("Body")
	h.click("publish")
	jobs, err := h.app.Store.PendingPublications(context.Background())
	if err != nil || len(jobs) != 1 {
		t.Fatal("missing job")
	}
	h.app.runPublication(context.Background(), h.bot, &jobs[0])
	d := h.active()
	if d.GitState != "failed" || !d.Locked() || d.Exported {
		t.Fatal("failed Git push marked published")
	}
	op := d.GitOperation
	h.send("/setchannel @other")
	repo.mu.Lock()
	repo.fail = false
	repo.mu.Unlock()
	h.click("publish")
	jobs, err = h.app.Store.PendingPublications(context.Background())
	if err != nil || len(jobs) != 1 {
		t.Fatal("retry missing")
	}
	h.app.runPublication(context.Background(), h.bot, &jobs[0])
	d = h.active()
	if d.ID != first.ID || d.GitOperation != op || d.GitState != "done" || d.ChannelID != 0 {
		t.Fatal("retry changed operation or channel")
	}
}
