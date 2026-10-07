package post

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestPhotoCaptionsAreExplicitOnlyInExport(t *testing.T) {
	first, second := ImageAsset("first", "jpg"), ImageAsset("second", "png")
	d := Draft{Portfolio: true, Number: 269, Slug: "title", PublishedAt: time.Date(2026, 10, 7, 0, 0, 0, 0, time.UTC), Title: "Title", Summary: "Summary", Content: "Body", Category: "concept"}
	// These source references also represent drafts saved before caption support.
	d.Number = 0
	for _, source := range []Source{
		{MessageID: 3, Media: &Media{Kind: "photo", Asset: second, FileID: "second", GroupID: "album", Origin: "https://t.me/source/2"}},
		{MessageID: 2, Media: &Media{Kind: "photo", Asset: first, FileID: "first", GroupID: "album"}, Text: "**Caption** with [link](https://example.com).\n\nSecond paragraph."},
	} {
		if err := d.Append(source); err != nil {
			t.Fatal(err)
		}
	}
	d.Number = 269
	image1, image2 := "![]("+ImageURLDir+first+")", "![]("+ImageURLDir+second+")"
	caption := "**Caption** with [link](https://example.com).\n\nSecond paragraph."
	plain := "Body\n\n" + image1 + "\n\n" + caption + "\n\n" + image2 + "\n\n[Source](https://t.me/source/2)"
	if d.Content != plain || d.TelegramContent() != plain {
		t.Fatal("draft presentation changed or caption detached from its image")
	}
	before, _ := json.Marshal(d)
	raw, err := d.Markdown()
	if err != nil {
		t.Fatal(err)
	}
	_, body, _ := strings.Cut(string(raw), "\n---\n\n")
	want := "Body\n\n" + image1 + "\n\n:::caption\n" + caption + "\n:::\n\n" + image2 + "\n\n[Source](https://t.me/source/2)"
	if body != want {
		t.Fatalf("wrong exported image/caption/source ordering:\n%s", body)
	}
	after, _ := json.Marshal(d)
	if string(before) != string(after) {
		t.Fatal("export changed the saved draft")
	}
	article, err := ReadArticle("content/tweets/269-title.md", string(raw))
	if err != nil || article.TelegramContent() != plain || !SameArticle(d, article.Draft) {
		t.Fatal("reload changed presentation or lost source association", err)
	}
	d.Frontmatter, d.Filename, d.Revision = article.Frontmatter, article.Filename, &Revision{Original: string(raw)}
	unchanged, err := d.Markdown()
	if err != nil || string(unchanged) != string(raw) {
		t.Fatal("opening an unchanged revision rewrote the article", err)
	}
	d.Title = "New title"
	revised, err := d.Markdown()
	if err != nil || !strings.HasSuffix(string(revised), want) || !strings.Contains(string(revised), "2026-10-07") {
		t.Fatal("revision lost caption markup or publication date", err)
	}
	d.Portfolio, d.Frontmatter = false, ""
	local, err := d.Markdown()
	if err != nil || !strings.HasSuffix(string(local), want) {
		t.Fatal("local download omitted caption markup", err)
	}
}

func TestCaptionDelimitersPreserveLiteralTextAndFormatting(t *testing.T) {
	image := "![Alt text](" + ImageURLDir + ImageAsset("photo", "jpg") + ")"
	for _, tc := range []struct{ caption, block string }{
		{"Caption", ":::caption\nCaption\n:::"},
		{"**Bold**\n\n[Link](https://example.com)", ":::caption\n**Bold**\n\n[Link](https://example.com)\n:::"},
		{"Literal\n::: \n::::\nend", ":::::caption\nLiteral\n::: \n::::\nend\n:::::"},
		{"```text\n:::\n```", "::::caption\n```text\n:::\n```\n::::"},
		{"- Code\n\n  ```text\n  :::\n  ```", "::::caption\n- Code\n\n  ```text\n  :::\n  ```\n::::"},
		{"Caption\n", ":::caption\nCaption\n\n:::"},
	} {
		t.Run(tc.caption, func(t *testing.T) {
			if got := ImageCaption(tc.caption); got != tc.block {
				t.Fatalf("unsafe caption block: %q", got)
			}
			d := Draft{Content: image + "\n\n" + tc.block + "\n\nOrdinary paragraph."}
			if got := d.TelegramContent(); got != image+"\n\n"+tc.caption+"\n\nOrdinary paragraph." {
				t.Fatalf("caption text/alt/formatting changed: %q", got)
			}
		})
	}
}

func TestCaptionMarkersOnlyHideInValidImageBlocks(t *testing.T) {
	image := "![](" + ImageURLDir + ImageAsset("photo", "jpg") + ")"
	valid := image + "\n\n:::caption\nCaption\n:::"
	for _, text := range []string{
		image + "\n\nOrdinary paragraph.",
		image + "\n\n:::caption\nUnclosed caption",
		":::caption\nNo image\n:::",
		"Text " + valid,
		"Text\n" + valid,
		image + "\n:::caption\nNot a separate block\n:::",
		"```markdown\n" + valid + "\n```",
		"~~~\n" + valid + "\n~~~",
		"`code\n" + valid + "\ncode`",
		"\\" + valid,
		image + "\n\n    :::caption\n    Code\n    :::",
	} {
		if got := (Draft{Content: text}).TelegramContent(); got != text {
			t.Fatalf("ordinary/malformed/code content changed: %q -> %q", text, got)
		}
	}
	for _, block := range []string{":::caption\n:::", ":::caption\n```text\n:::\n```\n:::"} {
		got := (Draft{Content: image + "\n\n" + block}).TelegramContent()
		if strings.Contains(got, "caption") {
			t.Fatal("structural markers leaked", got)
		}
		if strings.Contains(block, "```text") && !strings.Contains(got, "```text\n:::\n```") {
			t.Fatal("literal closing marker in code was removed", got)
		}
	}
}

func TestNonImageCaptionsAndUnmarkedArticlesRemainOrdinary(t *testing.T) {
	d := Draft{Title: "Title", Summary: "Summary", Content: "Body", Category: "concept"}
	if err := d.Append(Source{MessageID: 1, Media: &Media{Kind: "video", Origin: "https://video.example/watch"}, Text: "Video caption"}); err != nil {
		t.Fatal(err)
	}
	raw, err := d.PortfolioMarkdown()
	if err != nil || strings.Contains(string(raw), ":::caption") || !strings.HasSuffix(string(raw), d.Content) {
		t.Fatal("video caption became an image caption", err)
	}
	d.Content, d.Sources = "![]("+ImageURLDir+ImageAsset("old", "jpg")+")\n\nAn ordinary paragraph.", nil
	raw, err = d.PortfolioMarkdown()
	if err != nil || !strings.HasSuffix(string(raw), d.Content) {
		t.Fatal("legacy article paragraphs were guessed as captions", err)
	}
}
