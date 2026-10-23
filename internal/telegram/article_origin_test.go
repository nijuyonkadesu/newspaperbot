package telegram

import (
	"context"
	"strings"
	"testing"
	"time"

	"newspaperbot/internal/post"
)

func TestReusedOriginNumberDoesNotResumePreviousArticle(t *testing.T) {
	for _, command := range []string{"/posts", "/edit 268"} {
		t.Run(command, func(t *testing.T) {
			h := newHarness(t)
			original := exampleArticle(t, 268, time.Date(2020, 1, 2, 0, 0, 0, 0, time.UTC))
			repo := &fakeRepository{entries: []post.Article{original}}
			h.app.Repository = repo
			h.send("/edit 268")
			d := h.active()
			d.Title, d.Content = "Cached pending title", "Cached pending body"
			d.ChannelID, d.ContentMessageID, d.Delivery = -1001, 501, "sent"
			if err := h.app.Store.Save(context.Background(), &d); err != nil {
				t.Fatal(err)
			}
			replacement := original
			replacement.Title, replacement.Content = "Title from origin", "Body from origin"
			replacement.Filename, replacement.Slug = "content/tweets/268-replacement.md", "replacement"
			replacement.Frontmatter = ""
			raw, err := replacement.PortfolioMarkdown()
			if err != nil {
				t.Fatal(err)
			}
			replacement.Original = string(raw)
			repo.entries = []post.Article{replacement}
			h.send(command)
			if command == "/posts" {
				list := h.api.snapshot()
				text := list[len(list)-1].Text
				if !strings.Contains(text, "Title from origin") || strings.Contains(text, "Cached") || strings.Contains(text, "Editing") {
					t.Fatal("post list borrowed the previous article's content or editing state", text)
				}
				h.clickPosts("Preview #268")
				preview := h.api.snapshot()
				markdown := preview[len(preview)-1].RichMarkdown
				if !strings.Contains(markdown, "Body from origin") || strings.Contains(markdown, "Cached") {
					t.Fatal("post preview used cached article content", markdown)
				}
				h.send("/edit 268")
			}
			current := h.active()
			if current.ID == d.ID || current.Title != "Title from origin" || current.Content != "Body from origin" || current.ChannelID != 0 || current.ContentMessageID != 0 {
				t.Fatalf("replacement inherited the old editor or delivery links: %+v", current)
			}
		})
	}
}

func TestPostsPreviewUsesOriginWhileLocalEditsRemainPending(t *testing.T) {
	h := newHarness(t)
	article := exampleArticle(t, 268, time.Date(2020, 1, 2, 0, 0, 0, 0, time.UTC))
	repo := &fakeRepository{entries: []post.Article{article}}
	h.app.Repository = repo
	h.send("/edit 268")
	pending := h.active()
	pending.Title, pending.Content = "Pending local title", "Pending local body"
	if err := h.app.Store.Save(context.Background(), &pending); err != nil {
		t.Fatal(err)
	}
	article.Title, article.Content = "Current origin title", "Current origin body"
	raw, err := article.PortfolioMarkdown()
	if err != nil {
		t.Fatal(err)
	}
	article.Original = string(raw)
	repo.entries = []post.Article{article}
	h.send("/posts")
	list := h.api.snapshot()
	if text := list[len(list)-1].Text; !strings.Contains(text, "Current origin title") || strings.Contains(text, "Pending local title") {
		t.Fatal("list used local pending content", text)
	}
	h.clickPosts("Preview #268")
	preview := h.api.snapshot()
	if markdown := preview[len(preview)-1].RichMarkdown; !strings.Contains(markdown, "Current origin body") || strings.Contains(markdown, "Pending local body") {
		t.Fatal("live preview used pending edit content", markdown)
	}
	if saved := h.draft(pending.ID); saved.Content != "Pending local body" || saved.Revision == nil {
		t.Fatal("reading origin discarded pending edits to the same article")
	}
}
