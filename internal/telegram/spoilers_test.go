package telegram

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/go-telegram/bot/models"
)

func TestSpoilerEntitiesKeepNestedFormattingLinksAndUTF16Offsets(t *testing.T) {
	text, incomplete := entityMarkdown("🙂 hidden link", []models.MessageEntity{
		{Type: models.MessageEntityTypeSpoiler, Offset: 3, Length: 11},
		{Type: models.MessageEntityTypeBold, Offset: 3, Length: 6},
		{Type: models.MessageEntityTypeTextLink, Offset: 10, Length: 4, URL: "https://example.com/hidden"},
	})
	if incomplete || text != "🙂 ||**hidden** [link](https://example.com/hidden)||" {
		t.Fatal("spoiler conversion lost formatting/link or added review", text, incomplete)
	}
}

func TestSpoilerImportsKeepBoundaryWhitespaceOutsideDelimiters(t *testing.T) {
	text, incomplete := entityMarkdown("Before  hidden  after", []models.MessageEntity{
		{Type: models.MessageEntityTypeSpoiler, Offset: 7, Length: 8},
	})
	if incomplete || text != "Before  ||hidden||  after" {
		t.Fatal("entity whitespace prevented spoiler rendering", text, incomplete)
	}
	var rich models.RichMessage
	if err := json.Unmarshal([]byte(`{"blocks":[{"type":"paragraph","text":["Before ",{"type":"spoiler","text":" hidden "}," after"]}]}`), &rich); err != nil {
		t.Fatal(err)
	}
	text, issue, _ := importRichMessage(&rich)
	if text != "Before  ||hidden||  after" || issue != "" {
		t.Fatal("rich whitespace prevented spoiler rendering", text, issue)
	}
}

func TestForwardedRichSpoilerStaysHiddenWithoutReview(t *testing.T) {
	var rich models.RichMessage
	if err := json.Unmarshal([]byte(`{"blocks":[{"type":"paragraph","text":["Visible ",{"type":"spoiler","text":[{"type":"bold","text":"hidden"}," ",{"type":"url","text":"link","url":"https://example.com/hidden"}]}]}]}`), &rich); err != nil {
		t.Fatal(err)
	}
	text, issue, _ := importRichMessage(&rich)
	if text != "Visible ||**hidden** [link](https://example.com/hidden)||" || issue != "" {
		t.Fatal("rich spoiler lost content or required review", text, issue)
	}
	h := newHarness(t)
	d := h.ready("Body")
	h.click("preview")
	sends := h.api.count("sendMessage", false)
	h.forwardRich(rich, d.CardID)
	d = h.active()
	if len(d.MessageIssues) != 0 || !d.Preview || h.api.count("sendMessage", false) != sends || !strings.Contains(h.api.live()[0].RichMarkdown, text) {
		t.Fatal("spoiler did not stay in the existing rich preview")
	}
}
