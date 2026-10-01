package post

import (
	"strings"
	"testing"
	"time"

	"go.yaml.in/yaml/v3"
)

func TestPortfolioFormatAndNewLabels(t *testing.T) {
	d := taxonomyDraft()
	if err := d.Replace(Source{Text: "A title: 日本語\n\nSummary: \"quoted\"\n\n## Body\n\n    preserve indentation\n\nCategory: new category\nTags: future-tag, go"}); err != nil {
		t.Fatal(err)
	}
	if d.CategoryLabel() != "new category*" || strings.Join(d.TagLabels(), ",") != "future-tag*,go" {
		t.Fatal("new labels not marked individually")
	}
	d.Portfolio, d.Slug, d.PublishedAt = true, "a-title", time.Date(2026, 10, 1, 23, 0, 0, 0, time.UTC)
	data, err := d.Markdown()
	if err != nil {
		t.Fatal(err)
	}
	front, body, ok := strings.Cut(string(data)[4:], "\n---\n\n")
	if !strings.HasPrefix(string(data), "---\ntype: tweet\n") || !ok || body != d.Content {
		t.Fatal("body or frontmatter delimiters changed")
	}
	var fields map[string]any
	if err := yaml.Unmarshal([]byte(front), &fields); err != nil {
		t.Fatal(err)
	}
	if len(fields) != 7 || fields["title"] != d.Title || fields["summary"] != d.Summary || fields["date"] != "2026-10-01" || fields["category"] != d.Category || strings.Contains(front, "future-tag*") {
		t.Fatalf("wrong frontmatter %+v", fields)
	}
	d.Categories = append(d.Categories, d.Category)
	d.AvailableTags = append(d.AvailableTags, "future-tag")
	if d.CategoryLabel() != d.Category || strings.Join(d.TagLabels(), ",") != "future-tag,go" {
		t.Fatal("published labels still marked new")
	}
}

func TestCategoryGroupsSuggestWithoutRestrictingTags(t *testing.T) {
	d := Draft{Category: "concept", AvailableTags: []string{"a", "b", "c"}, TagGroups: map[string][]string{"concept": {"c", "a"}, "other": {"a"}}}
	d.OrderTags()
	if strings.Join(d.AvailableTags, ",") != "c,a,b" {
		t.Fatal("groups filtered or duplicated tags")
	}
}
