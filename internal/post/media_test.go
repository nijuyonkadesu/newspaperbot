package post

import (
	"strings"
	"testing"
)

func TestImageRewritingLeavesOrdinaryMarkdownAndCodeIntact(t *testing.T) {
	name := ImageAsset("photo", "jpg")
	image := "![description](" + ImageURLDir + name + ")"
	for _, code := range []string{"`" + image + "`", "``" + image + "``", "`line\n" + image + "`", "`pair `` then " + image + "`", "```markdown\n" + image + "\n```", "~~~\n" + image + "\n~~~", "    " + image, "\\" + image, "[link](" + ImageURLDir + name + ")"} {
		if got := RewriteImages(code, func(string, string) string { return "REPLACED" }); got != code {
			t.Fatalf("changed literal/ordinary Markdown: %q -> %q", code, got)
		}
	}
	external := "![external](https://site.test/a.jpg) · "
	got := RewriteImages(external+image+" trailing", func(asset, alt string) string {
		if asset != name || alt != "description" {
			t.Fatal("wrong image/alt", asset, alt)
		}
		return "REPLACED"
	})
	if got != external+"REPLACED trailing" {
		t.Fatal("lost surrounding image or text", got)
	}
}

func TestCaptionNeverActsAsPostOrTaxonomyAndAlbumKeepsAllMembers(t *testing.T) {
	d := Draft{Step: Review, Title: "Title", Summary: "Summary", Content: "Body", Category: "concept", Categories: []string{"concept", "personal"}, AvailableTags: []string{"go"}, Tags: []string{"go"}}
	for _, id := range []int{5, 3} {
		m := &Media{Kind: "photo", FileID: "photo", PhotoID: "photo", Asset: ImageAsset(string(rune(id)), "jpg"), GroupID: "album"}
		if err := d.Append(Source{MessageID: id, Media: m, Text: "Caption\nCategory: personal\nTags: -"}); err != nil {
			t.Fatal(err)
		}
	}
	if d.Category != "concept" || len(d.Tags) != 1 || strings.Count(d.Content, "Caption") != 2 || len(d.ImageFiles()) != 2 {
		t.Fatal("caption changed taxonomy or album dropped a member")
	}
	if strings.Index(d.Content, d.Sources[1].Media.Asset) > strings.Index(d.Content, d.Sources[0].Media.Asset) {
		t.Fatal("album was not ordered by message ID")
	}
	if err := d.RemoveSource(3); err != nil {
		t.Fatal(err)
	}
	if len(d.ImageFiles()) != 1 || strings.Count(d.Content, "Caption") != 1 {
		t.Fatal("removal retained an image or caption")
	}
}

func TestMediaBeforeTextSurvivesInitialComposition(t *testing.T) {
	d := Draft{Step: Compose, Category: "concept"}
	if err := d.Append(Source{MessageID: 1, Media: &Media{Kind: "photo", Asset: ImageAsset("own", "jpg"), FileID: "photo"}, Text: "Caption"}); err != nil {
		t.Fatal(err)
	}
	if d.Step != Compose {
		t.Fatal("media incorrectly supplied title/summary")
	}
	if err := d.Replace(Source{MessageID: 2, Text: "Title\n\nSummary\n\nBody"}); err != nil {
		t.Fatal(err)
	}
	if len(d.ImageFiles()) != 1 || !strings.Contains(d.Content, "Caption") || d.Validate() != nil {
		t.Fatal("initial post message discarded its previously supplied image")
	}
}
