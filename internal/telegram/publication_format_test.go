package telegram

import (
	"strings"
	"testing"
)

func TestPublicationHashtagsDoNotBecomeArticleOrDMContent(t *testing.T) {
	for _, destination := range []string{"@first", "@group"} {
		for _, tc := range []struct {
			name, config, footer string
		}{
			{"useful", "#useful", "\n\n\\#useful"},
			{"custom", " #reading_list  #keep ", "\n\n\\#reading\\_list \\#keep"},
			{"disabled", "", ""},
		} {
			t.Run(destination+"/"+tc.name, func(t *testing.T) {
				h := newHarness(t)
				repo := &fakeRepository{}
				h.app.Repository, h.app.PublishHashtags = repo, tc.config
				h.send("/setchannel " + destination)
				h.ready("Body\nCategory: development\nTags: go")
				h.click("preview")
				h.send("/publish")
				h.finishGit()
				d := h.active()
				h.api.mu.Lock()
				channel := h.api.messages[d.ContentMessageID]
				h.api.mu.Unlock()
				if channel.RichMarkdown != "# Title\n\nSummary\n\nBody"+tc.footer {
					t.Fatal("destination footer missing or changed article formatting", channel.RichMarkdown)
				}
				h.send("/posts")
				h.clickPosts("Preview #269")
				h.send("/download 269")
				h.send("/edit 269")
				h.send("Changed body")
				h.click("save")
				h.finishGit()
				updated := h.active()
				h.api.mu.Lock()
				channel = h.api.messages[updated.ContentMessageID]
				h.api.mu.Unlock()
				if updated.ContentMessageID != d.ContentMessageID || !strings.Contains(channel.RichMarkdown, "Changed body") || !strings.HasSuffix(channel.RichMarkdown, tc.footer) {
					t.Fatal("saving edits lost the footer or replaced the channel message", channel.RichMarkdown)
				}
				if updated.Category != "development" || len(updated.Tags) != 1 || updated.Tags[0] != "go" {
					t.Fatal("publication hashtags changed article taxonomy")
				}
				for _, marker := range []string{"#useful", "#reading", "#keep"} {
					if strings.Contains(repo.entries[0].Original, marker) || strings.Contains(updated.Content, marker) {
						t.Fatal("publication hashtag leaked into Git or stored article content")
					}
					for _, call := range h.api.snapshot() {
						if call.ChatID == h.app.OwnerID && strings.Contains(call.Text+call.RichMarkdown+call.Document, marker) {
							t.Fatal("publication hashtag leaked into a DM preview or download")
						}
					}
				}
			})
		}
	}
}

func TestPublicationHashtagCountsTowardRichLimitAndStaysOutOfDocument(t *testing.T) {
	h := newHarness(t)
	h.app.PublishHashtags = "#useful"
	h.send("/setchannel @first")
	const prefix = "# Title\n\nSummary\n\n"
	h.ready(strings.Repeat("x", 32768-len(prefix)))
	h.send("/publish")
	d := h.active()
	if !d.DocumentMode || d.Delivery != "sent" {
		t.Fatal("publication limit ignored its appended hashtag")
	}
	h.api.mu.Lock()
	summary, document := h.api.messages[d.SummaryMessageID], h.api.messages[d.ContentMessageID]
	h.api.mu.Unlock()
	if summary.Text != "Title\n\nSummary\n\n#useful" || strings.Contains(document.Document, "#useful") {
		t.Fatal("document delivery lost its public hashtag or changed the exported Markdown")
	}
}
