package telegram

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/go-telegram/bot/models"
	"newspaperbot/internal/linkpreview"
	"newspaperbot/internal/post"
)

type richSample struct {
	Name        string
	Markdown    string
	RichMessage models.RichMessage `json:"rich_message"`
}

// These are real Bot API responses, not outputs synthesized by the importer.
// Only the temporary photo IDs were replaced with deterministic test IDs.
func richSamples(t *testing.T) []richSample {
	t.Helper()
	data, err := os.ReadFile("../../testdata/rich_forward.json")
	if err != nil {
		t.Fatal(err)
	}
	var samples []richSample
	if err := json.Unmarshal(data, &samples); err != nil {
		t.Fatal(err)
	}
	return samples
}

func TestRealRichPreviewResponsesImportWithoutLosingContent(t *testing.T) {
	for _, sample := range richSamples(t) {
		t.Run(sample.Name, func(t *testing.T) {
			text, issue, images := importText(&models.Message{RichMessage: &sample.RichMessage})
			if text == "" {
				t.Fatal("rich content disappeared")
			}
			needsReview := sample.Name == "details" || sample.Name == "unsupported-format"
			if (issue != "") != needsReview {
				t.Fatalf("unexpected review state: %q", issue)
			}
			d := post.Draft{Content: text, Images: images}
			switch sample.Name {
			case "generated-preview", "generated-web-card":
				original := post.Draft{Slot: 1, Title: "Round-trip preview", Summary: "A short summary.", Category: "development", Categories: []string{"development"}, Tags: []string{"go"}, AvailableTags: []string{"go"},
					Content: "## Section\nA **bold** idea with [link](https://example.com).\n\n> Quoted text.\n\n- first\n- second\n\n```go\nfmt.Println(\"hello\")\n```"}
				generated := previewMarkdown(original)
				if sample.Name == "generated-web-card" {
					generated = appendLinkCard(generated, linkpreview.Card{Title: "Web card title", URL: "https://example.com/card", Description: "Card description."}, true, true)
					if len(images) != 1 || len(d.ImageFiles()) != 1 || !strings.Contains(text, "[**Web card title**](https://example.com/card)") {
						t.Fatal("generated URL card lost its image or link")
					}
				}
				if sample.Markdown != generated {
					t.Fatal("captured API input no longer matches our generated preview")
				}
				for _, fragment := range []string{"# Round-trip preview", "## Section", "**bold**", "[link](https://example.com/)", "```go\nfmt.Println(\"hello\")\n```", "`#1`", "**Category:** development", "**Tags:** go"} {
					if !strings.Contains(text, fragment) {
						t.Fatalf("preview lost %q", fragment)
					}
				}
			case "nested-links":
				for _, fragment := range []string{"**bold *italic***", "🙂", "[**Link \\[label\\]**](https://example.com/a%5C%28b%5C%29?x=1&y=2)", "[email](mailto:user@example.com)", "[phone](tel:+123456789)"} {
					if !strings.Contains(text, fragment) {
						t.Fatalf("link/formatting changed: missing %q in %q", fragment, text)
					}
				}
			case "literal-code":
				if !strings.Contains(text, "`**literal**`") || !strings.Contains(text, "`` `tick` ``") || !strings.Contains(text, "&amp; text") || strings.Contains(text, "and \\&") {
					t.Fatal("literal text/code gained escaping artifacts", text)
				}
			case "lists-quotes":
				for _, fragment := range []string{"3. third", "4. fourth", "- nested one", "- [ ] pending", "- [x] done", "> **Quote**"} {
					if !strings.Contains(text, fragment) {
						t.Fatal("list numbering, checkbox, nesting, or quote lost", text)
					}
				}
			case "table":
				if !strings.Contains(text, "| Heading | Value |\n| :--- | ---: |") || !strings.Contains(text, "A \\| B") || !strings.Contains(text, "**C**") {
					t.Fatal("table structure or cell formatting lost", text)
				}
			case "captioned-photo":
				if len(d.ImageFiles()) != 1 || !strings.Contains(text, ":::caption\nCaption with **emphasis** and [link](https://example.com/caption).\n:::") || !strings.Contains(text, "\n\nCredit\n\nOrdinary text.") {
					t.Fatal("photo/caption association or attribution lost", text)
				}
				if strings.Contains(d.TelegramContent(), ":::caption") {
					t.Fatal("caption export markers leaked into Telegram")
				}
			case "unsupported-format":
				if !strings.Contains(text, "[link](https://example.com/u)") || !strings.Contains(text, "||spoiler||") || !strings.Contains(text, "highlight") {
					t.Fatal("unsupported styling lost accessible text or its nested link")
				}
			case "details":
				if !strings.Contains(text, "More **information**") || !strings.Contains(text, "[link](https://example.com/details)") {
					t.Fatal("unsupported layout discarded its nested content")
				}
			}
		})
	}
}

func (h *harness) forwardRich(message models.RichMessage, reply int) int {
	h.t.Helper()
	h.messageID++
	m := &models.Message{ID: h.messageID, From: &models.User{ID: 42}, Chat: models.Chat{ID: 42, Type: models.ChatTypePrivate}, RichMessage: &message, ForwardOrigin: &models.MessageOrigin{Type: models.MessageOriginTypeUser}}
	if reply != 0 {
		m.ReplyToMessage = &models.Message{ID: reply}
	}
	h.app.Handle(context.Background(), h.bot, &models.Update{ID: int64(m.ID), Message: m})
	return m.ID
}

func TestForwardedRichMessageEditsAndRemovalStayWithTheirCard(t *testing.T) {
	h := newHarness(t)
	first := h.ready("Original body")
	h.click("preview")
	first = h.active()
	var rich models.RichMessage
	for _, sample := range richSamples(t) {
		if sample.Name == "generated-web-card" {
			rich = sample.RichMessage
		}
	}
	second := h.ready("Second body")
	sends := h.api.count("sendMessage", false)
	source := h.forwardRich(rich, first.CardID)
	d := h.draft(first.ID)
	if d.CardID != first.CardID || !d.Preview || len(d.ImageFiles()) != 1 || len(d.MessageIssues) != 0 || !strings.Contains(d.Content, "Web card title") || h.active().ID != second.ID || h.api.count("sendMessage", false) != sends || h.api.count("getFile", false) != 0 {
		t.Fatal("forward lost content/media, fetched files, or changed its selected card")
	}
	h.restart()
	var edited models.RichMessage
	if err := json.Unmarshal([]byte(`{"blocks":[{"type":"paragraph","text":["Edited ",{"type":"url","text":"link","url":"https://example.com/edited"}]}]}`), &edited); err != nil {
		t.Fatal(err)
	}
	h.app.Handle(context.Background(), h.bot, &models.Update{ID: 999, EditedMessage: &models.Message{ID: source, EditDate: 1, From: &models.User{ID: 42}, Chat: models.Chat{ID: 42, Type: models.ChatTypePrivate}, RichMessage: &edited}})
	d = h.draft(first.ID)
	if d.Content != "Original body\n\nEdited [link](https://example.com/edited)" || len(d.ImageFiles()) != 0 || h.active().ID != second.ID || d.CardID != first.CardID || !d.Preview {
		t.Fatal("rich edit after restart lost its routing or retained removed media", d.Content)
	}
	h.replyTo(source, "/remove")
	if d = h.draft(first.ID); d.Content != "Original body" || d.CardID != first.CardID || !d.Preview || h.active().ID != second.ID {
		t.Fatal("rich source removal changed the wrong draft or its preview state")
	}
}

func TestUnsupportedAndMalformedRichContentKeepsValidSiblings(t *testing.T) {
	for _, input := range []string{
		`{"blocks":[{"type":"paragraph","text":"Kept"},{"type":"future_block"}]}`,
		`{"blocks":[{"type":"paragraph","text":["Kept",{"type":"future_text"}]}]}`,
		`{"blocks":[{"type":"paragraph","text":"Kept"},{"type":"photo","photo":[]}]}`,
		`{"blocks":[{"type":"paragraph","text":"Kept"},{"type":"heading","size":999}]}`,
	} {
		var rich models.RichMessage
		if err := json.Unmarshal([]byte(input), &rich); err != nil {
			t.Fatal(err)
		}
		text, _, _ := importRichMessage(&rich)
		if !strings.Contains(text, "Kept") {
			t.Fatal("malformed rich block dropped a valid sibling")
		}
	}
	var deep models.RichMessage
	input := `"Kept"`
	for range 20 {
		input = `{"type":"bold","text":` + input + `}`
	}
	if err := json.Unmarshal([]byte(`{"blocks":[{"type":"paragraph","text":`+input+`}]}`), &deep); err != nil {
		t.Fatal(err)
	}
	if _, issue, _ := importRichMessage(&deep); issue == "" {
		t.Fatal("excessive nesting was not reported for review")
	}
	h := newHarness(t)
	d := h.ready("Body")
	h.click("preview")
	var mixed models.RichMessage
	if err := json.Unmarshal([]byte(`{"blocks":[{"type":"paragraph","text":"Kept"},{"type":"future_block"}]}`), &mixed); err != nil {
		t.Fatal(err)
	}
	sends := h.api.count("sendMessage", false)
	id := h.forwardRich(mixed, d.CardID)
	d = h.active()
	if d.Content != "Body\n\nKept" || len(d.MessageIssues) != 1 || d.MessageIssues[0].MessageID != id || h.api.count("sendMessage", false) != sends || !strings.Contains(h.api.live()[0].RichMarkdown, "**Review**") {
		t.Fatal("unsupported content was silently skipped or review escaped the existing preview")
	}
}
