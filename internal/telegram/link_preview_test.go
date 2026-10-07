package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-telegram/bot/models"
	"newspaperbot/internal/linkpreview"
	"newspaperbot/internal/post"
)

func previewWebsite(t *testing.T, withImage bool) (*httptest.Server, []byte, *atomic.Int32) {
	t.Helper()
	var photo bytes.Buffer
	if err := png.Encode(&photo, image.NewRGBA(image.Rect(0, 0, 24, 16))); err != nil {
		t.Fatal(err)
	}
	requests := &atomic.Int32{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.URL.Path == "/photo" {
			w.Header().Set("Content-Type", "image/png")
			w.Write(photo.Bytes())
			return
		}
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<title>Fallback title</title><meta property=og:title content="Remote title"><meta property=og:description content="Preview description">`))
		if withImage {
			w.Write([]byte(`<meta property=og:image content="/photo">`))
		}
	}))
	t.Cleanup(server.Close)
	return server, photo.Bytes(), requests
}

func TestCustomLinkPreviewPreservesMarkdownCardAndDownload(t *testing.T) {
	server, photo, requests := previewWebsite(t, true)
	h := newHarness(t)
	h.app.Previews = linkpreview.New(server.Client())
	body := "### Section\nKeep **bold** and [the link](" + server.URL + ")."
	d := h.ready(body)
	before, err := d.Markdown()
	if err != nil {
		t.Fatal(err)
	}
	h.click("preview")
	h.waitPreviews()
	calls := h.api.snapshot()
	call := calls[len(calls)-1]
	base := "# Title\n\nSummary\n\n" + body
	if call.MessageID != d.CardID || call.Text != "" || call.ParseMode != "" || !strings.HasPrefix(call.RichMarkdown, base+"\n\n---\n\n") ||
		!strings.Contains(call.RichMarkdown, "![](tg://photo?id=newspaperbot_preview)") ||
		!strings.Contains(call.RichMarkdown, "**[Remote title]("+server.URL+")**") ||
		!strings.HasSuffix(call.RichMarkdown, "\n\n---\n\n`#1`\n\n**Category:** development\n\n**Tags:** none") {
		t.Fatal("custom card replaced rich formatting, taxonomy, or message identity", call.RichMarkdown)
	}
	if !bytes.Equal(call.RichImage, photo) || len(call.RichMedia) != 1 {
		t.Fatal("thumbnail was not uploaded as rich-message media")
	}
	var media struct {
		ID    string `json:"id"`
		Media struct {
			Type, Media string
		}
	}
	if err := json.Unmarshal(call.RichMedia[0], &media); err != nil || media.ID != "newspaperbot_preview" || media.Media.Type != "photo" || media.Media.Media != "attach://newspaperbot-preview.png" {
		t.Fatal("invalid rich media attachment", err, media)
	}
	got := h.active()
	after, err := got.Markdown()
	if err != nil || !bytes.Equal(before, after) || got.Content != body || got.CardID != d.CardID || !got.Preview || got.Notice != "" || len(h.api.live()) != 1 {
		t.Fatal("link preview modified stored content, view, or card lifecycle", err)
	}
	h.send("A second paragraph.")
	h.waitPreviews()
	if requests.Load() != 2 || h.active().CardID != d.CardID || !h.active().Preview {
		t.Fatal("editing refetched metadata or replaced its preview card")
	}
	h.send("/download")
	calls = h.api.snapshot()
	document := calls[len(calls)-1].Document
	if !strings.Contains(document, body+"\n\nA second paragraph.") || strings.Contains(document, "Remote title") || strings.Contains(document, "newspaperbot_preview") {
		t.Fatal("preview-only metadata leaked into downloaded Markdown")
	}
}

func TestCustomPreviewFailureLeavesOriginalRichDraft(t *testing.T) {
	for _, mode := range []string{"no metadata", "not found", "unreachable", "timeout", "rejected card"} {
		t.Run(mode, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch mode {
				case "not found":
					http.NotFound(w, r)
				case "timeout":
					<-r.Context().Done()
				case "rejected card":
					w.Header().Set("Content-Type", "text/html")
					w.Write([]byte("<title>Remote title</title>"))
				default:
					w.Header().Set("Content-Type", "text/html")
					w.Write([]byte("<html><body>No metadata</body></html>"))
				}
			}))
			defer server.Close()
			client := server.Client()
			if mode == "unreachable" {
				server.Close()
			}
			if mode == "timeout" {
				client.Timeout = 30 * time.Millisecond
			}
			h := newHarness(t)
			h.app.Previews = linkpreview.New(client)
			d := h.ready("### Section\n**Keep formatting** [and link](" + server.URL + ").")
			base := previewMarkdown(d)
			if mode == "rejected card" {
				h.api.rejectRichContaining = "**[Remote title]"
			}
			h.click("preview")
			h.waitPreviews()
			// Rejected edit attempts don't change the card already on screen.
			live := h.api.live()
			call := live[0]
			if call.RichMarkdown != base || call.Text != "" || call.ParseMode != "" || call.MessageID != d.CardID ||
				h.active().Notice != "" || h.active().Content != d.Content || !h.active().Preview || len(h.api.live()) != 1 {
				t.Fatal("optional card failure broke rich preview or lost draft content", call.RichMarkdown)
			}
			h.send("Still writing.")
			h.waitPreviews()
			if !h.active().Preview || h.active().Content != d.Content+"\n\nStill writing." {
				t.Fatal("metadata failure prevented further drafting")
			}
		})
	}
}

func TestRejectedThumbnailFallsBackToTextAndIsNotUploadedAgain(t *testing.T) {
	server, _, requests := previewWebsite(t, true)
	h := newHarness(t)
	h.app.Previews = linkpreview.New(server.Client())
	d := h.ready("[site](" + server.URL + ")")
	h.api.imageRejected = true
	h.click("preview")
	h.waitPreviews()
	calls := h.api.snapshot()
	call := calls[len(calls)-1]
	if len(call.RichImage) != 0 || !strings.Contains(call.RichMarkdown, "**[Remote title]") || strings.Contains(call.RichMarkdown, "tg://photo") || h.active().Notice != "" {
		t.Fatal("image rejection lost usable metadata or switched to normal text")
	}
	uploads := 0
	for _, call := range calls {
		if len(call.RichImage) != 0 {
			uploads++
		}
	}
	h.send("Keep writing.")
	h.waitPreviews()
	after := h.api.snapshot()
	for _, call := range after[len(calls):] {
		if len(call.RichImage) != 0 {
			t.Fatal("rejected thumbnail was uploaded on every edit")
		}
	}
	if uploads != 1 || requests.Load() != 2 || h.active().CardID != d.CardID || h.active().Content != d.Content+"\n\nKeep writing." {
		t.Fatal("fallback refetched the website or crossed draft identities")
	}
}

func TestCustomCardCannotExceedRichLimit(t *testing.T) {
	server, _, _ := previewWebsite(t, true)
	h := newHarness(t)
	h.app.Previews = linkpreview.New(server.Client())
	d := h.ready("[site](" + server.URL + ")")
	body := d.Content + "\n\n" + strings.Repeat("x", 32768-len(previewMarkdown(d))-3)
	h.edit(d.Sources[0].MessageID, "Title\n\nSummary\n\n"+body)
	d = h.active()
	base := previewMarkdown(d)
	h.click("preview")
	h.waitPreviews()
	calls := h.api.snapshot()
	call := calls[len(calls)-1]
	if call.RichMarkdown != base || h.active().Notice != "" || call.MessageID != d.CardID {
		t.Fatalf("a long optional card broke an otherwise valid rich message: want %d chars, got %d, notice %q, card %d/%d", len(base), len(call.RichMarkdown), h.active().Notice, call.MessageID, d.CardID)
	}
}

func TestArticleAndDraftShareCustomPreviewWithoutChangingSelection(t *testing.T) {
	server, _, requests := previewWebsite(t, false)
	h := newHarness(t)
	h.app.Previews = linkpreview.New(server.Client())
	article := exampleArticle(t, 268, time.Date(2020, 1, 2, 0, 0, 0, 0, time.UTC))
	article.Content = "### Article\n[site](" + server.URL + ")"
	raw, err := article.Markdown()
	if err != nil {
		t.Fatal(err)
	}
	article.Original = string(raw)
	h.app.Repository = &fakeRepository{entries: []post.Article{article}}
	draft := h.ready("Separate draft")
	h.send("/posts")
	listID, _ := h.app.Store.Setting(t.Context(), "posts_message")
	h.clickPosts("Preview #268")
	h.waitPreviews()
	calls := h.api.snapshot()
	call := calls[len(calls)-1]
	if call.MessageID != int(listID) || !strings.Contains(call.RichMarkdown, "**[Remote title]") || !strings.Contains(call.RichMarkdown, "**Live** · `#268`") ||
		h.active().ID != draft.ID || requests.Load() != 1 {
		t.Fatal("article URL card lost identity or selected the article for editing")
	}
	if revisions, err := h.app.Store.Revisions(context.Background()); err != nil || len(revisions) != 0 {
		t.Fatal("preview-only metadata opened an article revision", err)
	}
	h.clickPosts("Edit #268")
	h.click("preview")
	h.waitPreviews()
	calls = h.api.snapshot()
	if !strings.Contains(calls[len(calls)-1].RichMarkdown, "**[Remote title]") || h.active().Revision == nil || requests.Load() != 1 {
		t.Fatal("article editing used a different renderer or discarded cached metadata")
	}
}

func TestPreviewMetadataCannotInjectRichHTML(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<meta property=og:title content="&lt;tg-button data=evil&gt;Click&lt;/tg-button&gt; [title]">`))
	}))
	defer server.Close()
	h := newHarness(t)
	h.app.Previews = linkpreview.New(server.Client())
	h.ready("[site](" + server.URL + ")")
	h.click("preview")
	h.waitPreviews()
	calls := h.api.snapshot()
	markdown := calls[len(calls)-1].RichMarkdown
	if strings.Contains(markdown, "<tg-button") || !strings.Contains(markdown, "\\<tg\\-button") || !strings.Contains(markdown, "\\[title\\]") || h.active().Notice != "" {
		t.Fatal("untrusted page title became active rich content", markdown)
	}
}

func TestCustomPreviewDoesNotRetryUncertainOrRateLimitedRequests(t *testing.T) {
	for _, code := range []int{403, 429, 500} {
		t.Run(http.StatusText(code), func(t *testing.T) {
			server, _, _ := previewWebsite(t, false)
			h := newHarness(t)
			h.app.Previews = linkpreview.New(server.Client())
			d := h.ready("[site](" + server.URL + ")")
			card := h.app.Previews.Lookup(t.Context(), previewMarkdown(d))
			h.api.rejectRichContaining, h.api.rejectRichCode = "Remote title", code
			before := len(h.api.snapshot())
			_, err := h.app.writeLinkCard(t.Context(), h.bot, previewTarget{h.app.OwnerID, d.CardID}, draftKeyboard(d, true),
				&models.InputRichMessage{Markdown: previewMarkdown(d)}, card, true)
			if err == nil || len(h.api.snapshot()) != before+1 {
				t.Fatal("non-rejection failure retried a different payload", err)
			}
		})
	}
}

func TestCustomPreviewRecreatesOnlyAMissingCard(t *testing.T) {
	server, _, requests := previewWebsite(t, true)
	h := newHarness(t)
	h.app.Previews = linkpreview.New(server.Client())
	h.ready("[site](" + server.URL + ")")
	h.click("preview")
	h.waitPreviews()
	d := h.active()
	callback := keyboard(d, "Preview", "preview").InlineKeyboard[0][0].CallbackData
	h.api.mu.Lock()
	delete(h.api.messages, d.CardID)
	h.api.mu.Unlock()
	h.callback(callback)
	h.waitPreviews()
	if current := h.active(); current.CardID == d.CardID || !current.Preview || current.Content != d.Content || current.Notice != "" || len(h.api.live()) != 1 || requests.Load() != 2 {
		t.Fatal("custom preview lost missing-card recovery or refetched metadata")
	}
	calls := h.api.snapshot()
	call := calls[len(calls)-1]
	if call.Method != "editMessageText" || call.MessageID != h.active().CardID || len(call.RichImage) == 0 || !strings.Contains(call.RichMarkdown, "Remote title") {
		t.Fatal("replacement card was not enriched in place using native rich media")
	}
}

// Wait for this render's enrichment explicitly; foreground helpers stay instant.
func (h *harness) waitPreviews() {
	h.t.Helper()
	h.app.mu.Lock()
	jobs := make([]*previewJob, 0, len(h.app.previewJobs))
	for _, job := range h.app.previewJobs {
		jobs = append(jobs, job)
	}
	h.app.mu.Unlock()
	for _, job := range jobs {
		select {
		case <-job.done:
		case <-time.After(2 * time.Second):
			h.t.Fatal("preview job did not finish")
		}
	}
}
