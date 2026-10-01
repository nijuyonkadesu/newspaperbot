package post

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestComposerSourcesAndReplacement(t *testing.T) {
	d := Draft{Category: "development"}
	body := "## Heading\n\n**bold**\n\n```go\nfmt.Println(1)\n```\n"
	if err := d.Replace(Source{MessageID: 1, UpdateID: 1, Text: "# A title\n\nA summary\n\n" + body}); err != nil {
		t.Fatal(err)
	}
	if d.Content != body || d.Title != "A title" {
		t.Fatal("source parsing changed content")
	}
	if err := d.Append(Source{MessageID: 2, UpdateID: 2, Text: "  addition\n"}); err != nil {
		t.Fatal(err)
	}
	if err := d.Replace(Source{Text: "incomplete replacement"}); err == nil || d.Content != body+"\n\n  addition\n" {
		t.Fatal("invalid replacement lost content")
	}
	d.EditSource(Source{MessageID: 1, UpdateID: 3, Text: "Edited title\n\nEdited summary\n\nEdited body"})
	if d.Content != "Edited body\n\n  addition\n" {
		t.Fatal("editing root lost addition")
	}
	d.EditSource(Source{MessageID: 2, UpdateID: 4, Text: "Revised addition"})
	if d.Content != "Edited body\n\nRevised addition" {
		t.Fatal("editing addition appended again")
	}
	if d.EditSource(Source{MessageID: 2, UpdateID: 3, Text: "old"}) {
		t.Fatal("replayed older edit accepted")
	}
	d.EditSource(Source{MessageID: 1, UpdateID: 5, Text: "Invalid edit"})
	if d.Validate() == nil || d.Content != "Edited body\n\nRevised addition" {
		t.Fatal("invalid edit discarded valid content or allowed publishing")
	}
	d.EditSource(Source{MessageID: 1, UpdateID: 6, Text: "Fixed title\n\nFixed summary\n\nFixed body"})
	if err := d.Undo(); err != nil || d.Content != "Fixed body" {
		t.Fatal("undo failed")
	}
	if err := d.Undo(); err == nil {
		t.Fatal("undo removed original post")
	}
	d.Number = 43
	if d.EditSource(Source{MessageID: 1, UpdateID: 7, Text: "Locked"}) || d.Replace(Source{Text: "New\n\nSummary\n\nBody"}) == nil {
		t.Fatal("publication remained editable")
	}
}

func TestParseSourceValidation(t *testing.T) {
	for _, text := range []string{"", "Title", "Title\n\nSummary", "Title\n\nSummary\n\n  ", strings.Repeat("🙂", 201) + "\n\nSummary\n\nBody"} {
		if _, _, _, err := ParseSource(text); err == nil {
			t.Fatalf("invalid source accepted: %q", text)
		}
	}
	for _, text := range []string{"Title\nSummary\n\n  Body\n", "# Title\n\nSummary\n\n  Body\n", "/newpost Title\n\nSummary\n\n  Body\n", "/newpost@bot\nTitle\n\nSummary\n\n  Body\n"} {
		title, summary, body, err := ParseSource(text)
		if err != nil || title != "Title" || summary != "Summary" || body != "  Body\n" {
			t.Fatalf("bad parsing: %q %q %q %v", title, summary, body, err)
		}
	}
	_, _, body, err := ParseSource("Title\r\n\r\nSummary\r\n\r\n  Body\r\n")
	if err != nil || body != "  Body\r\n" {
		t.Fatal("CRLF body changed")
	}
}

func TestLegacyNormalizationPreservesPendingBody(t *testing.T) {
	for _, current := range []string{"", "Current body"} {
		d := Draft{Title: "Title", Summary: "Summary", Categories: []string{"development"}, Content: current, PendingContent: "Pending replacement", Step: "content", Editing: current != ""}
		d.Normalize()
		if d.Category != "development" || d.Step != Review {
			t.Fatal("legacy draft did not normalize")
		}
		if current == "" && d.Content != "Pending replacement" || current != "" && (d.Content != current || d.PendingContent != "Pending replacement") {
			t.Fatal("normalization lost unfinished content")
		}
	}
}

func TestNativeEditOrderingAfterUpdateIDReset(t *testing.T) {
	d := Draft{Category: "development"}
	if err := d.Replace(Source{MessageID: 1, UpdateID: 1000, Text: "Title\n\nSummary\n\nBody"}); err != nil {
		t.Fatal(err)
	}
	if !d.EditSource(Source{MessageID: 1, UpdateID: 20, EditDate: 100, Text: "Title\n\nSummary\n\nNew body"}) {
		t.Fatal("new edit with a randomized update ID was rejected")
	}
	if d.EditSource(Source{MessageID: 1, UpdateID: 2000, EditDate: 99, Text: "Title\n\nSummary\n\nOld replay"}) || d.Content != "New body" {
		t.Fatal("old edit replay overrode the latest edit")
	}
}

func TestMarkdownPreservesSourceAndEscapesMetadata(t *testing.T) {
	d := Draft{Number: 43, Title: "Title \"quoted\"\nline", Summary: "a\\b", Category: "development", Tags: []string{}, PublishedAt: time.Now().UTC(), Content: "\n# Header\n\n```go\n// **literal**\n```\n"}
	data, err := d.Markdown()
	if err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	var metadata map[string]any
	if err := decoder.Decode(&metadata); err != nil {
		t.Fatal(err)
	}
	if metadata["title"] != d.Title || metadata["number"] != float64(43) {
		t.Fatalf("bad metadata: %v", metadata)
	}
	if string(data[decoder.InputOffset()+2:]) != d.Content {
		t.Fatal("Markdown body changed")
	}
}

func TestAtomicExportAndNumberScan(t *testing.T) {
	dir := t.TempDir()
	name := filepath.Join(dir, "43.md")
	if err := WriteFile(name, []byte("first")); err != nil {
		t.Fatal(err)
	}
	if err := WriteFile(name, []byte("first")); err != nil {
		t.Fatal("recovery failed:", err)
	}
	if err := WriteFile(name, []byte("different")); err == nil {
		t.Fatal("collision was overwritten")
	}
	data, err := os.ReadFile(name)
	if err != nil || string(data) != "first" {
		t.Fatal("existing file changed")
	}
	if err := os.WriteFile(filepath.Join(dir, "README.md"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	number, err := HighestNumber(dir)
	if err != nil || number != 43 {
		t.Fatalf("number = %d, error = %v", number, err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 2 {
		t.Fatal("temporary export files leaked")
	}
}
