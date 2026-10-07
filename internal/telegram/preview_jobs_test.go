package telegram

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/go-telegram/bot"
	"newspaperbot/internal/linkpreview"
	"newspaperbot/internal/post"
)

func signal(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("operation did not finish")
	}
}

func TestMarkdownAppearsBeforeURLFetchAndSwitchCancelsIt(t *testing.T) {
	started, cancelled := make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-r.Context().Done()
		close(cancelled)
	}))
	t.Cleanup(server.Close)
	h := newHarness(t)
	h.app.Previews = linkpreview.New(server.Client())
	d := h.ready("### Section\n[site](" + server.URL + ")")
	returned := make(chan struct{})
	go func() { h.click("preview"); close(returned) }()
	signal(t, returned)
	signal(t, started)
	calls := h.api.snapshot()
	call := calls[len(calls)-1]
	if call.RichMarkdown != previewMarkdown(d) || call.MessageID != d.CardID {
		t.Fatal("first paint waited for or changed website content", call.RichMarkdown)
	}
	h.send("/newpost")
	signal(t, cancelled)
	if h.active().ID == d.ID {
		t.Fatal("switch was blocked by the website fetch")
	}
	for _, call := range h.api.snapshot() {
		if strings.Contains(call.RichMarkdown, "Remote title") {
			t.Fatal("cancelled post received a late edit")
		}
	}
}

type previewTransport func(*http.Request) (*http.Response, error)

func (f previewTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestSupersededFetchCannotOverwriteEditedSourceEvenIfTransportIgnoresCancel(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	h := newHarness(t)
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})
	h.app.Previews = linkpreview.New(&http.Client{Transport: previewTransport(func(r *http.Request) (*http.Response, error) {
		close(started)
		<-release // Deliberately ignore request cancellation.
		return &http.Response{StatusCode: 200, Header: http.Header{}, Request: r, Body: io.NopCloser(strings.NewReader("<title>Old website title</title>"))}, nil
	})})
	d := h.ready("[site](https://site.test)")
	h.click("preview")
	signal(t, started)
	h.app.mu.Lock()
	old := h.app.previewJobs[previewTarget{h.app.OwnerID, d.CardID}]
	h.app.mu.Unlock()
	h.edit(d.Sources[0].MessageID, "Title\n\nSummary\n\n### New body\nNo URL now.")
	before := len(h.api.snapshot())
	close(release)
	signal(t, old.done)
	calls := h.api.snapshot()
	if len(calls) != before || !strings.Contains(calls[len(calls)-1].RichMarkdown, "### New body") || h.active().CardID != d.CardID {
		t.Fatal("old fetch overwrote the newer source or its preview")
	}
}

func TestReturningToPostsCancelsPendingArticlePreview(t *testing.T) {
	started, cancelled := make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { close(started); <-r.Context().Done(); close(cancelled) }))
	t.Cleanup(server.Close)
	h := newHarness(t)
	h.app.Previews = linkpreview.New(server.Client())
	article := exampleArticle(t, 268, time.Date(2020, 1, 2, 0, 0, 0, 0, time.UTC))
	article.Content = "[site](" + server.URL + ")"
	h.app.Repository = &fakeRepository{entries: []post.Article{article}}
	h.send("/posts")
	h.clickPosts("Preview #268")
	signal(t, started)
	h.clickPosts("Back")
	signal(t, cancelled)
	calls := h.api.snapshot()
	if !strings.HasPrefix(calls[len(calls)-1].Text, "<b>Live articles</b>") {
		t.Fatal("article list was replaced by a late preview")
	}
}

func TestPublishedURLCardEditsDestinationWithoutTouchingMarkdown(t *testing.T) {
	for _, destination := range []string{"@first", "@group"} {
		t.Run(destination, func(t *testing.T) {
			server, photo, _ := previewWebsite(t, true)
			h := newHarness(t)
			h.app.Previews = linkpreview.New(server.Client())
			h.send("/setchannel " + destination)
			h.ready("[site](" + server.URL + ")\n\n---\n\n### Closing section\nKeep this after the divider.")
			h.send("/publish")
			h.waitPreviews()
			d := h.active()
			var sends, edits int
			var final apiCall
			for _, call := range h.api.snapshot() {
				if call.ChatID != d.ChannelID {
					continue
				}
				if call.Method == "sendRichMessage" {
					sends++
				}
				if call.Method == "editMessageText" {
					edits++
					final = call
				}
			}
			if d.Delivery != "sent" || sends != 1 || edits != 1 || final.MessageID != d.ContentMessageID ||
				!strings.HasPrefix(final.RichMarkdown, d.RichMarkdown()+"\n\n---\n\n") || !strings.Contains(final.RichMarkdown, "**[Remote title]") || !bytes.Equal(final.RichImage, photo) {
				t.Fatal("destination did not use the shared URL card or misplaced article content", final.RichMarkdown)
			}
			expected, err := d.Markdown()
			if err != nil {
				t.Fatal(err)
			}
			saved, err := os.ReadFile(d.Filename)
			if err != nil || !bytes.Equal(saved, expected) || bytes.Contains(saved, []byte("Remote title")) {
				t.Fatal("URL card leaked into saved Markdown", err)
			}
		})
	}
}

func TestDestinationEnrichmentSurvivesOwnerSwitch(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		select {
		case <-release:
			w.Write([]byte("<title>Destination title</title>"))
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(server.Close)
	h := newHarness(t)
	h.app.Previews = linkpreview.New(server.Client())
	h.send("/setchannel @first")
	h.ready("[site](" + server.URL + ")")
	returned := make(chan struct{})
	go func() { h.send("/publish"); close(returned) }()
	signal(t, returned)
	signal(t, started)
	d := h.active()
	if d.Delivery != "sent" {
		t.Fatal("publication was waiting for the optional embed")
	}
	h.send("/newpost")
	close(release)
	h.waitPreviews()
	found := false
	for _, call := range h.api.snapshot() {
		if call.ChatID == d.ChannelID && call.MessageID == d.ContentMessageID && strings.Contains(call.RichMarkdown, "Destination title") {
			found = true
		}
	}
	if !found {
		t.Fatal("switching owner posts cancelled the independent destination update")
	}
}

func TestDocumentSummaryGetsURLCardButAttachmentStaysOriginal(t *testing.T) {
	server, _, _ := previewWebsite(t, false)
	h := newHarness(t)
	h.app.Previews = linkpreview.New(server.Client())
	h.send("/setchannel @first")
	h.ready("[site](" + server.URL + ")\n" + strings.Repeat("x", 32769))
	h.send("/publish")
	h.waitPreviews()
	d := h.active()
	expected, err := d.Markdown()
	if err != nil {
		t.Fatal(err)
	}
	summary, document := false, false
	for _, call := range h.api.snapshot() {
		if call.ChatID != d.ChannelID {
			continue
		}
		if call.Method == "editMessageText" && call.MessageID == d.SummaryMessageID && strings.Contains(call.RichMarkdown, "**[Remote title]") {
			summary = true
		}
		if call.Method == "sendDocument" && call.Document == string(expected) {
			document = true
		}
	}
	if !summary || !document || d.Delivery != "sent" {
		t.Fatal("document publication lost its URL summary or changed the Markdown")
	}
}

func TestClosingPreviewsCancelsFetchesAndJoinsThem(t *testing.T) {
	started, cancelled := make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { close(started); <-r.Context().Done(); close(cancelled) }))
	t.Cleanup(server.Close)
	h := newHarness(t)
	h.app.Previews = linkpreview.New(server.Client())
	h.ready("[site](" + server.URL + ")")
	h.click("preview")
	signal(t, started)
	h.app.ClosePreviews()
	signal(t, cancelled)
	h.app.mu.Lock()
	pending := len(h.app.previewJobs)
	h.app.mu.Unlock()
	if pending != 0 {
		t.Fatal(fmt.Sprintf("%d preview tasks survived shutdown", pending))
	}
}

func TestSwitchCancelsAnInFlightEnrichmentEdit(t *testing.T) {
	server, _, _ := previewWebsite(t, false)
	h := newHarness(t)
	h.app.Previews = linkpreview.New(server.Client())
	started, cancelled := make(chan struct{}), make(chan struct{})
	transport := previewTransport(func(r *http.Request) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Path, "/editMessageText") {
			if err := r.ParseMultipartForm(1 << 20); err != nil && err != http.ErrNotMultipart {
				return nil, err
			}
			if strings.Contains(r.FormValue("rich_message"), "Remote title") {
				close(started)
				<-r.Context().Done()
				close(cancelled)
				return nil, r.Context().Err()
			}
		}
		return h.api.RoundTrip(r)
	})
	b, err := bot.New("123:test", bot.WithSkipGetMe(), bot.WithServerURL("https://telegram.test"), bot.WithHTTPClient(time.Second, &http.Client{Transport: transport}))
	if err != nil {
		t.Fatal(err)
	}
	h.bot = b
	d := h.ready("[site](" + server.URL + ")")
	// A ready cache entry must not put media upload back on the foreground path.
	h.app.Previews.Lookup(t.Context(), d.RichMarkdown())
	h.click("preview")
	signal(t, started)
	returned := make(chan struct{})
	go func() { h.send("/newpost"); close(returned) }()
	signal(t, cancelled)
	signal(t, returned)
}

func TestSavingLiveArticleEnrichesSameChannelMessage(t *testing.T) {
	server, _, _ := previewWebsite(t, false)
	h := newHarness(t)
	h.app.Previews = linkpreview.New(server.Client())
	article := exampleArticle(t, 268, time.Date(2020, 1, 2, 0, 0, 0, 0, time.UTC))
	h.app.Repository = &fakeRepository{entries: []post.Article{article}}
	h.send("/edit 268")
	d := h.active()
	d.ChannelID, d.ContentMessageID, d.Delivery = -1001, 500, "sent"
	if err := h.app.Store.Save(context.Background(), &d); err != nil {
		t.Fatal(err)
	}
	h.api.mu.Lock()
	h.api.messages[500] = apiCall{ChatID: -1001, ResultID: 500}
	h.api.mu.Unlock()
	h.send("[site](" + server.URL + ")")
	h.click("save")
	h.finishGit()
	h.waitPreviews()
	d = h.active()
	h.api.mu.Lock()
	updated := h.api.messages[500]
	h.api.mu.Unlock()
	if d.Revision != nil || d.Number != 268 || d.PublishedAt.Format("2006-01-02") != "2020-01-02" || !strings.Contains(updated.RichMarkdown, "**[Remote title]") ||
		h.api.count("sendRichMessage", true) != 0 {
		t.Fatal("saving lost the URL card, recreated the channel message, or changed article identity")
	}
	if strings.Contains(d.Content, "Remote title") {
		t.Fatal("web metadata became article content")
	}
}
