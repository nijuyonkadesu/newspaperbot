package telegram

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"newspaperbot/internal/post"
)

func TestPhotoPreviewUsesValidDistinctMediaIDsAndKeepsRichCard(t *testing.T) {
	h := newHarness(t)
	d := h.ready("## Section\nFormatted **body** stays rich.")
	h.click("preview")
	cardID := d.CardID
	for i := 0; i < post.MaxImages; i++ {
		h.sendMedia(photoMessage(fmt.Sprintf("photo-%d", i), fmt.Sprintf("Caption %d", i), cardID))
	}
	d = h.active()
	h.api.mu.Lock()
	card := h.api.messages[cardID]
	h.api.mu.Unlock()
	if d.CardID != cardID || d.View != "preview" || !d.Preview || d.Notice != "" || card.RichMarkdown == "" || !strings.Contains(card.RichMarkdown, "## Section\nFormatted **body**") || len(card.RichMedia) != post.MaxImages {
		t.Fatal("adding a photo degraded the rich preview or lost its state", d.Notice)
	}
	seen := map[string]bool{}
	for _, raw := range card.RichMedia {
		var ref struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(raw, &ref); err != nil {
			t.Fatal(err)
		}
		if len(ref.ID) == 0 || len(ref.ID) > 64 || seen[ref.ID] || !strings.Contains(card.RichMarkdown, "tg://photo?id="+ref.ID+")") {
			t.Fatal("invalid, duplicated, or unreferenced media ID")
		}
		seen[ref.ID] = true
	}
	if h.api.count("getFile", false) != 0 || h.api.count("sendMessage", false) != 1 {
		t.Fatal("preview retrieved image bytes or added chat messages")
	}
}

func TestImageDocumentPreviewUsesValidMediaReference(t *testing.T) {
	for _, thumbnail := range []bool{false, true} {
		t.Run(fmt.Sprintf("thumbnail=%t", thumbnail), func(t *testing.T) {
			h := newHarness(t)
			d := h.ready("Body")
			file := &models.Document{FileID: "document", FileUniqueID: "unique-document", MimeType: "image/png"}
			if thumbnail {
				file.Thumbnail = &models.PhotoSize{FileID: "thumbnail"}
			}
			h.sendMedia(models.Message{Document: file, ReplyToMessage: &models.Message{ID: d.CardID}, Caption: "Document caption"})
			h.click("preview")
			d = h.active()
			h.api.mu.Lock()
			card := h.api.messages[d.CardID]
			h.api.mu.Unlock()
			if d.Notice != "" || card.RichMarkdown == "" || len(card.RichMedia) != 1 || !strings.Contains(card.RichMarkdown, "Document caption") {
				t.Fatal("document fell back from the rich preview", d.Notice)
			}
			if h.api.count("getFile", false) != 0 {
				t.Fatal("document preview downloaded its image")
			}
		})
	}
}

func TestAPIRejectsOverlongRichMediaIdentifiers(t *testing.T) {
	h := newHarness(t)
	_, err := h.bot.SendRichMessage(context.Background(), &bot.SendRichMessageParams{ChatID: h.app.OwnerID, RichMessage: models.InputRichMessage{
		Markdown: "![](tg://photo?id=" + strings.Repeat("a", 65) + ")",
		Media:    []models.InputRichMessageMedia{{ID: strings.Repeat("a", 65), Media: &models.InputMediaPhoto{Media: "photo"}}},
	}})
	if err == nil {
		t.Fatal("test API accepted an invalid Telegram media identifier")
	}
}

func TestRecoveredPhotoPreviewClearsPreviousRenderErrorInPlace(t *testing.T) {
	h := newHarness(t)
	d := h.ready("## Body")
	h.sendMedia(photoMessage("own", "Caption", d.CardID))
	d = h.active()
	d.Preview, d.View, d.Notice = true, "preview", markdownRenderFailure
	if err := h.app.Store.Save(context.Background(), &d); err != nil {
		t.Fatal(err)
	}
	before := len(h.api.snapshot())
	if err := h.app.render(context.Background(), h.bot, &d); err != nil {
		t.Fatal(err)
	}
	calls := h.api.snapshot()[before:]
	if len(calls) != 1 || calls[0].Method != "editMessageText" || calls[0].MessageID != d.CardID || calls[0].RichMarkdown == "" || strings.Contains(calls[0].RichMarkdown, "could not render") || h.active().Notice != "" {
		t.Fatal("recovered preview retained a stale rendering error or added chat messages")
	}
}
