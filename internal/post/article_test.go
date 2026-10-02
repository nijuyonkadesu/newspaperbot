package post

import (
	"strings"
	"testing"
	"time"

	"go.yaml.in/yaml/v3"
)

func TestArticleRevisionPreservesIdentityAndUnknownMetadata(t *testing.T) {
	raw := "---\ntype: tweet\ntitle: Old title\nslug: original-url\ndate: '2020-01-02' # original date\nsummary: Summary\ncategory: concept\ntags: [code]\ncustom:\n  pinned: true\n---\n\n    keep indentation\n"
	a, err := ReadArticle("content/tweets/1000-original.md", raw)
	if err != nil {
		t.Fatal(err)
	}
	d := a.Draft
	d.Revision = &Revision{Original: raw}
	unchanged, err := d.Markdown()
	if err != nil || string(unchanged) != raw {
		t.Fatal("opening a revision changed the article", err)
	}
	if err := d.Replace(Source{MessageID: 9, Text: "Changed title\n\nChanged summary\n\n    new body\n\nCategory: research\nTags: new-tag, code"}); err != nil {
		t.Fatal(err)
	}
	data, err := d.Markdown()
	if err != nil {
		t.Fatal(err)
	}
	front, body, _ := strings.Cut(string(data)[4:], "\n---\n\n")
	var fields map[string]any
	if err := yaml.Unmarshal([]byte(front), &fields); err != nil {
		t.Fatal(err)
	}
	if fields["date"] != "2020-01-02" || fields["slug"] != "original-url" || fields["type"] != "tweet" || fields["title"] != "Changed title" || fields["custom"].(map[string]any)["pinned"] != true || body != "    new body" {
		t.Fatalf("metadata or body lost: %s", data)
	}
	if !strings.Contains(front, "# original date") {
		t.Fatal("date comment lost")
	}
	d.PublishedAt = time.Now().UTC()
	if _, err := d.Markdown(); err == nil {
		t.Fatal("revision changed publication date")
	}
}
