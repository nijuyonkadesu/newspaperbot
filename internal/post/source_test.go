package post

import (
	"strings"
	"testing"
)

func taxonomyDraft() Draft {
	return Draft{Categories: []string{"development", "personal"}, AvailableTags: []string{"go", "sqlite"}, Category: "development", Tags: []string{}, Step: Compose}
}

func TestTaxonomyFooters(t *testing.T) {
	for _, tc := range []struct {
		name, body      string
		parsed, invalid bool
		category, tags  string
	}{
		{"labelled", "Body\n\nCategory: personal\nTags: go, sqlite, go\n", true, false, "personal", "go,sqlite"},
		{"bare", "Body\npersonal\ngo, sqlite", true, false, "personal", "go,sqlite"},
		{"none", "Body\ncategory: personal\ntags: -", true, false, "personal", ""},
		{"crlf", "Body\r\n\r\nCategory: personal\r\nTags: go\r\n", true, false, "personal", "go"},
		{"new category", "Body\nCategory: typo\nTags: go", true, false, "typo", "go"},
		{"new tag", "Body\nCategory: personal\nTags: typo", true, false, "personal", "typo"},
		{"empty tag", "Body\nCategory: personal\nTags:", false, true, "", ""},
		{"empty body", "Category: personal\nTags: go", false, true, "", ""},
		{"bare unknown", "Body\npersonal\nsome words", false, false, "", ""},
		{"one label", "Body\nCategory: personal\ngo", false, false, "", ""},
		{"fenced", "Body\n```text\nCategory: personal\nTags: go", false, false, "", ""},
		{"tilde fence", "Body\n~~~\nCategory: personal\nTags: go", false, false, "", ""},
		{"closed code", "Body\n```\nCategory: personal\nTags: go\n```", false, false, "", ""},
		{"indented code", "Body\n    personal\n    go", false, false, "", ""},
		{"tab code", "Body\n\tpersonal\n\tgo", false, false, "", ""},
		{"ordinary trailing newline", "Body\n\nParagraph\n", false, false, "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := taxonomyDraft()
			err := d.Replace(Source{MessageID: 1, UpdateID: 1, Text: "Title\n\nSummary\n\n" + tc.body})
			if (err != nil) != tc.invalid {
				t.Fatalf("unexpected error: %v", err)
			}
			if tc.invalid {
				if d.Title != "" || d.Category != "development" {
					t.Fatal("invalid replacement partially changed draft")
				}
				return
			}
			if tc.parsed {
				if d.Content != "Body" || d.Category != tc.category || strings.Join(d.Tags, ",") != tc.tags {
					t.Fatalf("wrong parsed body/metadata: %+v", d)
				}
				if strings.Contains(d.Sources[0].Text, "Category:") {
					t.Fatal("saved source retained interpreted footer")
				}
			} else if d.Content != tc.body || d.Category != "development" || len(d.Tags) != 0 {
				t.Fatal("ordinary body text was changed")
			}
		})
	}
}

func TestInvalidFooterEditBlocksPublishUntilThatSourceIsFixed(t *testing.T) {
	d := taxonomyDraft()
	if err := d.Replace(Source{MessageID: 1, UpdateID: 1, Text: "Title\n\nSummary\n\nBody"}); err != nil {
		t.Fatal(err)
	}
	d.EditSource(Source{MessageID: 1, UpdateID: 2, Text: "Title\n\nSummary\n\nChanged\nCategory: personal\nTags: invalid tag"})
	if d.Invalid == "" || d.Content != "Body" || d.Category != "development" {
		t.Fatal("invalid native edit overwrote valid content")
	}
	_ = d.Append(Source{MessageID: 2, UpdateID: 3, Text: "Addition"})
	if d.Validate() == nil {
		t.Fatal("append cleared invalid footer on another source")
	}
	d.EditSource(Source{MessageID: 1, UpdateID: 4, Text: "Title\n\nSummary\n\nChanged\nCategory: personal\nTags: sqlite"})
	if err := d.Validate(); err != nil {
		t.Fatal(err)
	}
	if d.Content != "Changed\n\nAddition" || d.Category != "personal" || strings.Join(d.Tags, ",") != "sqlite" {
		t.Fatal("source repair lost additions or metadata")
	}
	d.Tags, d.Category = []string{}, "development"
	d.EditSource(Source{MessageID: 2, UpdateID: 5, Text: "Revised addition"})
	if len(d.Tags) != 0 || d.Category != "development" {
		t.Fatal("editing an addition reapplied old root footer")
	}
}

func TestCatalogChangesDoNotReinterpretExistingBodyOnAppend(t *testing.T) {
	d := taxonomyDraft()
	text := "Body\npersonal\nfuture-tag"
	if err := d.Replace(Source{MessageID: 1, Text: "Title\n\nSummary\n\n" + text}); err != nil {
		t.Fatal(err)
	}
	d.AvailableTags = append(d.AvailableTags, "future-tag")
	if err := d.Append(Source{MessageID: 2, Text: "Addition"}); err != nil {
		t.Fatal(err)
	}
	if d.Content != text+"\n\nAddition" || d.Category != "development" {
		t.Fatal("catalog refresh silently reinterpreted existing body")
	}
}

func TestAppendedTaxonomyFooters(t *testing.T) {
	for _, tc := range []struct {
		name, text, body, category, tags string
	}{
		{"labelled", "Addition\n\nCategory: personal\nTags: go, sqlite, go", "Addition", "personal", "go,sqlite"},
		{"bare", "Addition\npersonal\ngo", "Addition", "personal", "go"},
		{"new values", "Addition\nCategory: research\nTags: new-tag", "Addition", "research", "new-tag"},
		{"no tags", "Addition\nCategory: personal\nTags: -", "Addition", "personal", ""},
		{"crlf", "Addition\r\nCategory: personal\r\nTags: sqlite\r\n", "Addition", "personal", "sqlite"},
		{"unknown prose", "Addition\npersonal\nunknown", "Addition\npersonal\nunknown", "development", ""},
		{"code", "```text\nCategory: personal\nTags: go", "```text\nCategory: personal\nTags: go", "development", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := taxonomyDraft()
			if err := d.Replace(Source{MessageID: 1, Text: "Title\n\nSummary\n\nBody"}); err != nil {
				t.Fatal(err)
			}
			if err := d.Append(Source{MessageID: 2, Text: tc.text}); err != nil {
				t.Fatal(err)
			}
			if d.Title != "Title" || d.Summary != "Summary" || d.Content != "Body\n\n"+tc.body || d.Category != tc.category || strings.Join(d.Tags, ",") != tc.tags {
				t.Fatalf("addition lost body or metadata: %+v", d)
			}
			if d.Sources[1].Full || d.Sources[1].Text != tc.body {
				t.Fatal("addition was treated as a whole post or retained its footer")
			}
		})
	}
}

func TestAppendedFooterEditAndRepair(t *testing.T) {
	d := taxonomyDraft()
	_ = d.Replace(Source{MessageID: 1, Text: "Title\n\nSummary\n\nBody"})
	if err := d.Append(Source{MessageID: 2, UpdateID: 1, Text: "Addition\nCategory: personal\nTags: invalid tag"}); err == nil {
		t.Fatal("invalid appended footer was accepted")
	}
	if d.Validate() == nil || d.Content != "Body" || d.Category != "development" || len(d.Sources) != 2 {
		t.Fatal("invalid addition was lost or partially applied")
	}
	_ = d.Append(Source{MessageID: 3, UpdateID: 2, Text: "More"})
	if d.Validate() == nil {
		t.Fatal("later addition cleared an invalid source")
	}
	d.EditSource(Source{MessageID: 2, UpdateID: 3, Text: "Fixed addition\nCategory: personal\nTags: sqlite"})
	if d.Validate() != nil || d.Content != "Body\n\nFixed addition\n\nMore" || d.Category != "personal" || strings.Join(d.Tags, ",") != "sqlite" {
		t.Fatal("edited addition did not repair its body and metadata")
	}
	d.EditSource(Source{MessageID: 2, UpdateID: 4, Text: "Invalid edit\nCategory: personal\nTags:"})
	if d.Validate() == nil || d.Content != "Body\n\nFixed addition\n\nMore" {
		t.Fatal("invalid edit overwrote the last valid body")
	}
	d.EditSource(Source{MessageID: 2, UpdateID: 5, Text: "Fixed again\npersonal\ngo"})
	d.Category, d.Tags = "development", nil // Subsequent card selections win.
	d.EditSource(Source{MessageID: 3, UpdateID: 6, Text: "More revised"})
	if d.Validate() != nil || d.Content != "Body\n\nFixed again\n\nMore revised" || d.Category != "development" || len(d.Tags) != 0 {
		t.Fatal("rebuilding reinterpreted an older addition's footer")
	}
}

func TestSingleLineContentIsNeverTaxonomy(t *testing.T) {
	for _, line := range []string{"personal", "go", "Category: personal", "Tags: go"} {
		t.Run(line, func(t *testing.T) {
			d := taxonomyDraft()
			if err := d.Replace(Source{MessageID: 1, UpdateID: 1, Text: "Title\n\nSummary\n\n" + line}); err != nil {
				t.Fatal(err)
			}
			if err := d.Append(Source{MessageID: 2, UpdateID: 2, Text: line}); err != nil {
				t.Fatal(err)
			}
			if d.Content != line+"\n\n"+line || d.Category != "development" || len(d.Tags) != 0 {
				t.Fatal("single-line body/addition was mistaken for a footer")
			}
			d.Number, d.Revision = 268, &Revision{}
			d.EditSource(Source{MessageID: 2, UpdateID: 3, Text: "personal"})
			if err := d.Append(Source{MessageID: 3, UpdateID: 4, Text: "go"}); err != nil {
				t.Fatal(err)
			}
			if d.Content != line+"\n\npersonal\n\ngo" || d.Category != "development" || len(d.Tags) != 0 {
				t.Fatal("separate single-line messages formed a footer in a live revision")
			}
		})
	}
}
