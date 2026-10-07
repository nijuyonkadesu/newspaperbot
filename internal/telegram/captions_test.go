package telegram

import (
	"context"
	"strings"
	"testing"

	"github.com/go-telegram/bot/models"
)

func TestCaptionExportPublishReloadAndEditKeepTelegramPresentation(t *testing.T) {
	h := newHarness(t)
	repo := &fakeRepository{}
	h.app.Repository = repo
	h.send("/setchannel @channel")
	d := h.ready("Body")
	photo := photoMessage("caption-photo", "Caption with **emphasis**.\n\nSecond paragraph.", d.CardID)
	photo.ForwardOrigin = &models.MessageOrigin{Type: models.MessageOriginTypeChannel, MessageOriginChannel: &models.MessageOriginChannel{Chat: models.Chat{Username: "source"}, MessageID: 12}}
	id := h.sendMedia(photo)
	h.click("preview")
	d = h.active()
	h.api.mu.Lock()
	card := h.api.messages[d.CardID]
	h.api.mu.Unlock()
	if strings.Contains(card.RichMarkdown, ":::caption") || !strings.Contains(card.RichMarkdown, photo.Caption) || !strings.Contains(card.RichMarkdown, "[Source](https://t.me/source/12)") || len(card.RichMedia) != 1 {
		t.Fatal("draft preview lost its caption, image, or source", card.RichMarkdown)
	}
	h.send("/download")
	calls := h.api.snapshot()
	if !strings.Contains(calls[len(calls)-1].Document, ":::caption\n"+photo.Caption+"\n:::\n\n[Source]") {
		t.Fatal("draft download omitted the explicit caption/source separation")
	}
	h.send("/publish")
	h.finishGit()
	d = h.active()
	if d.Delivery != "sent" || !strings.Contains(repo.entries[0].Original, ":::caption\n"+photo.Caption+"\n:::") {
		t.Fatal("publication did not export caption markup and deliver the post")
	}
	h.api.mu.Lock()
	channel := h.api.messages[d.ContentMessageID]
	h.api.mu.Unlock()
	if strings.Contains(channel.RichMarkdown, ":::caption") || !strings.Contains(channel.RichMarkdown, photo.Caption) || len(channel.RichMedia) != 1 {
		t.Fatal("caption markers leaked into the channel or its photo disappeared", channel.RichMarkdown)
	}
	h.send("/posts")
	h.clickPosts("Preview #269")
	listID, _ := h.app.Store.Setting(context.Background(), "posts_message")
	h.api.mu.Lock()
	preview := h.api.messages[int(listID)]
	h.api.mu.Unlock()
	if strings.Contains(preview.RichMarkdown, ":::caption") || !strings.Contains(preview.RichMarkdown, photo.Caption) {
		t.Fatal("repository article preview exposed caption markers", preview.RichMarkdown)
	}
	h.send("/edit 269")
	d = h.active()
	if len(d.Sources) != 2 {
		t.Fatal("exported caption markup broke original source association")
	}
	h.click("back")
	h.api.mu.Lock()
	normal := h.api.messages[d.CardID]
	h.api.mu.Unlock()
	if strings.Contains(normal.Text, ":::caption") || !strings.Contains(normal.Text, "Caption with **emphasis**.") {
		t.Fatal("normal view exposed markers or lost the caption", normal.Text)
	}
	h.restart()
	photo.ID, photo.EditDate, photo.From, photo.Chat = id, 1, &models.User{ID: 42}, models.Chat{ID: 42, Type: models.ChatTypePrivate}
	photo.Caption = "Edited **caption**."
	h.app.Handle(context.Background(), h.bot, &models.Update{ID: 999, EditedMessage: &photo})
	h.send("/save")
	h.finishGit()
	d = h.active()
	if !strings.Contains(repo.entries[0].Original, ":::caption\nEdited **caption**.\n:::") || strings.Contains(repo.entries[0].Original, "Second paragraph.") {
		t.Fatal("editing the original caption after restart failed to update Markdown")
	}
	h.api.mu.Lock()
	channel = h.api.messages[d.ContentMessageID]
	h.api.mu.Unlock()
	if strings.Contains(channel.RichMarkdown, ":::caption") || !strings.Contains(channel.RichMarkdown, photo.Caption) || len(channel.RichMedia) != 1 {
		t.Fatal("saving edits lost the channel caption/image or exposed markers", channel.RichMarkdown)
	}
}
