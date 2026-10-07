package telegram

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"newspaperbot/internal/linkpreview"
	"newspaperbot/internal/post"
)

type downloadRepository struct {
	*fakeRepository
	refreshes  int
	refreshErr error
}

func (r *downloadRepository) Refresh(context.Context) error {
	r.refreshes++
	return r.refreshErr
}

func TestDownloadNumberSendsPublishedBytesWithoutSelectingOrEditing(t *testing.T) {
	for _, state := range []string{"no selection", "draft selected", "pending article edits"} {
		t.Run(state, func(t *testing.T) {
			h := newHarness(t)
			article := exampleArticle(t, 1000, time.Date(2020, 1, 2, 0, 0, 0, 0, time.UTC))
			raw := strings.Replace(article.Original, "type: tweet", "type: tweet\ncustom: retained # comment", 1)
			raw = strings.ReplaceAll(raw, "\n", "\r\n")
			article, err := post.ReadArticle(article.Filename, raw)
			if err != nil {
				t.Fatal(err)
			}
			repo := &downloadRepository{fakeRepository: &fakeRepository{entries: []post.Article{article}}}
			h.app.Repository = repo
			var selected post.Draft
			switch state {
			case "draft selected":
				selected = h.ready("Unpublished body")
			case "pending article edits":
				h.send("/edit 1000")
				h.send("Unpublished addition")
				selected = h.active()
			}
			h.send("/posts")
			listID, _ := h.app.Store.Setting(context.Background(), "posts_message")
			before := len(h.api.snapshot())
			refreshes := repo.refreshes
			if selected.ID != 0 {
				h.replyTo(selected.CardID, "/download@newspaperbot 1000")
			} else {
				h.send("/download@newspaperbot 1000")
			}
			calls := h.api.snapshot()[before:]
			if len(calls) != 1 || calls[0].Method != "sendDocument" || calls[0].ChatID != h.app.OwnerID || calls[0].DocumentFilename != "1000-original.md" || calls[0].Document != raw {
				t.Fatal("download did not send the exact published file", calls)
			}
			if repo.refreshes != refreshes+1 || repo.calls != 0 {
				t.Fatal("download did not refresh main or triggered publication")
			}
			if selected.ID != 0 {
				if !reflect.DeepEqual(h.active(), selected) {
					t.Fatal("download changed selection or pending content")
				}
			} else if active, _ := h.app.Store.Setting(context.Background(), "active"); active != 0 {
				t.Fatal("download started an edit")
			}
			if after, _ := h.app.Store.Setting(context.Background(), "posts_message"); after != listID {
				t.Fatal("download replaced the article list")
			}
			if selected.ID != 0 {
				data, err := selected.Markdown()
				if err != nil {
					t.Fatal(err)
				}
				h.replyTo(selected.CardID, "/download")
				calls = h.api.snapshot()
				if calls[len(calls)-1].Document != string(data) {
					t.Fatal("plain download stopped exporting the selected content and pending edits")
				}
			}
		})
	}
}

func TestDownloadNumberRejectsInvalidInputWithoutExportingSelectedDraft(t *testing.T) {
	h := newHarness(t)
	selected := h.ready("Unpublished body")
	for _, command := range []string{"/download 0", "/download -1", "/download invalid", "/download 1.5", "/download 9223372036854775808", "/download 1 2"} {
		before := len(h.api.snapshot())
		h.send(command)
		calls := h.api.snapshot()[before:]
		if len(calls) != 1 || calls[0].Method != "sendMessage" || calls[0].ParseMode != "HTML" || !strings.Contains(calls[0].Text, "/download 269") {
			t.Fatal("invalid argument fell back to exporting the selected draft", command, calls)
		}
		if !reflect.DeepEqual(h.active(), selected) {
			t.Fatal("invalid download changed the selected draft")
		}
	}
}

func TestDownloadNumberUnavailableDoesNotAlterSelectedDraft(t *testing.T) {
	for _, state := range []string{"no repository", "missing article", "fetch failed"} {
		t.Run(state, func(t *testing.T) {
			h := newHarness(t)
			selected := h.ready("Unpublished body")
			if state != "no repository" {
				repo := &downloadRepository{fakeRepository: &fakeRepository{}}
				if state == "fetch failed" {
					repo.refreshErr = errors.New("upstream unavailable")
				}
				h.app.Repository = repo
			}
			before := len(h.api.snapshot())
			h.replyTo(selected.CardID, "/download 1000")
			calls := h.api.snapshot()[before:]
			if len(calls) != 1 || calls[0].Method != "sendMessage" || calls[0].ChatID != h.app.OwnerID {
				t.Fatal("unavailable article was silently ignored or exported a draft", calls)
			}
			if !reflect.DeepEqual(h.active(), selected) {
				t.Fatal("unavailable article changed the selected draft")
			}
		})
	}
}

func TestDownloadsDoNotCancelOrReplacePendingRichPreview(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		select {
		case <-release:
			w.Write([]byte("<title>Remote URL guide</title>"))
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(server.Close)
	h := newHarness(t)
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})
	h.app.Previews = linkpreview.New(server.Client())
	article := exampleArticle(t, 1000, time.Now().UTC())
	h.app.Repository = &fakeRepository{entries: []post.Article{article}}
	h.ready("[Guide](" + server.URL + ")")
	h.click("preview")
	signal(t, started)
	selected := h.active()
	job := h.app.ownerPreview.Load()
	h.send("/download 1000")
	h.replyTo(selected.CardID, "/download")
	if job == nil || h.app.ownerPreview.Load() != job || !reflect.DeepEqual(h.active(), selected) {
		t.Fatal("download cancelled the preview or changed the selected post")
	}
	close(release)
	signal(t, job.done)
	calls := h.api.snapshot()
	final := calls[len(calls)-1]
	if final.Method != "editMessageText" || final.MessageID != selected.CardID || !strings.Contains(final.RichMarkdown, "Remote URL guide") {
		t.Fatal("pending URL preview did not finish after the downloads")
	}
}
