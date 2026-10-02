package telegram

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/go-telegram/bot/models"
	"newspaperbot/internal/post"
)

func TestOriginalMessageQuotePreservesLivePreviewAndCleansUp(t *testing.T) {
	h := newHarness(t)
	h.app.Repository = &fakeRepository{}
	live := h.ready("Body")
	rootID := h.messageID
	h.click("publish")
	h.finishGit()
	action := fmt.Sprintf("source-%d", rootID)
	h.click(action)
	if h.active().SourceReplyID == 0 || h.active().Revision != nil {
		t.Fatal("published article could not locate its original message")
	}
	h.click("edit")
	if h.active().SourceReplyID != 0 || len(h.api.live()) != 1 {
		t.Fatal("opening a revision left its publication's quote behind")
	}
	h.click("preview")
	before := h.active()
	for i := 0; i < 2; i++ {
		h.click(action)
		d := h.active()
		if d.SourceReplyID == 0 || d.SourceReplyToID != rootID || d.CardID != live.CardID || !d.Preview || d.View != "preview" || !d.UpdatedAt.Equal(before.UpdatedAt) || d.Content != before.Content || d.Number != before.Number || !d.PublishedAt.Equal(before.PublishedAt) || d.Revision == nil {
			t.Fatal("locating the source altered its article or preview")
		}
		h.api.mu.Lock()
		quote := h.api.messages[d.SourceReplyID]
		preview := h.api.messages[d.CardID]
		h.api.mu.Unlock()
		if quote.ReplyParameters == nil || quote.ReplyParameters.MessageID != rootID || quote.ReplyParameters.AllowSendingWithoutReply || !quote.DisableNotification || quote.ParseMode != "HTML" || !strings.Contains(quote.Text, "Tap the quote") {
			t.Fatal("source navigation did not use a silent native reply")
		}
		if len(h.api.live()) != 2 || preview.RichMarkdown != previewMarkdown(before) {
			t.Fatal("repeated navigation accumulated quotes or changed the main preview")
		}
	}
	h.edit(rootID, "Changed title\n\nSummary\n\nRevised body")
	d := h.active()
	if d.Content != "Revised body" || d.SourceReplyID != 0 || d.CardID != live.CardID || !d.Preview || len(h.api.live()) != 1 {
		t.Fatal("native editing did not update the preview and remove its temporary quote")
	}
}

func TestRemoveByReplyToSourceQuoteDeletesItsActualSource(t *testing.T) {
	h := newHarness(t)
	d := h.ready("Body")
	h.click("preview")
	h.messageID++
	id := h.messageID
	h.app.Handle(context.Background(), h.bot, &models.Update{ID: int64(id), Message: &models.Message{
		ID: id, From: &models.User{ID: 42}, Chat: models.Chat{ID: 42, Type: models.ChatTypePrivate}, Text: "Addition",
		Entities: []models.MessageEntity{{Type: models.MessageEntityTypeUnderline, Offset: 0, Length: 8}},
	}})
	h.click(fmt.Sprintf("source-%d", id))
	quoteID := h.active().SourceReplyID
	cardID := d.CardID
	h.restart()
	h.replyTo(quoteID, "/remove")
	d = h.active()
	if d.Content != "Body" || d.SourceReplyID != 0 || d.SourceReplyToID != 0 || d.CardID != cardID || !d.Preview || len(h.api.live()) != 1 {
		t.Fatal("removing a quoted source did not restore the same preview")
	}
	deleted := map[int]bool{}
	for _, call := range h.api.snapshot() {
		if call.Method == "deleteMessage" {
			deleted[call.MessageID] = true
		}
	}
	if !deleted[id] || !deleted[quoteID] || !deleted[h.messageID] {
		t.Fatal("removal did not clear the source, temporary quote, and command")
	}
}

func TestSourceQuoteRoutesRepliesToItsDraftAfterRestart(t *testing.T) {
	h := newHarness(t)
	first := h.ready("First body")
	rootID := h.messageID
	h.click("preview")
	second := h.ready("Second body")
	action := fmt.Sprintf("source-%d", rootID)
	h.clickDraft(first.ID, action)
	quoteID := h.draft(first.ID).SourceReplyID
	h.restart()
	h.replyTo(quoteID, "Addition\nCategory: personal\nTags: go")
	d := h.draft(first.ID)
	if d.Content != "First body\n\nAddition" || d.Category != "personal" || strings.Join(d.Tags, ",") != "go" || !d.Preview || d.SourceReplyID != 0 || d.CardID != first.CardID {
		t.Fatal("replying to the quote lost the draft, taxonomy, or preview")
	}
	if h.active().ID != second.ID || h.draft(second.ID).Content != "Second body" || len(h.api.live()) != 2 {
		t.Fatal("source navigation changed another draft or left extra messages")
	}
	h.clickDraft(first.ID, action)
	if err := h.app.RestoreCard(context.Background(), h.bot); err != nil {
		t.Fatal(err)
	}
	if h.draft(first.ID).SourceReplyID != 0 || len(h.api.live()) != 2 {
		t.Fatal("restoring cards did not clean up the recorded quote")
	}
}

func TestReviewSourceUsesNativeReplyAndMissingSourceStaysOnCard(t *testing.T) {
	h := newHarness(t)
	h.ready("Body")
	h.click("preview")
	cardID := h.active().CardID
	h.messageID++
	id := h.messageID
	h.app.Handle(context.Background(), h.bot, &models.Update{ID: int64(id), Message: &models.Message{
		ID: id, From: &models.User{ID: 42}, Chat: models.Chat{ID: 42, Type: models.ChatTypePrivate}, RichMessage: &models.RichMessage{},
	}})
	action := fmt.Sprintf("source-%d", id)
	h.click(action)
	d := h.active()
	h.api.mu.Lock()
	quote := h.api.messages[d.SourceReplyID]
	h.api.mu.Unlock()
	if quote.ReplyParameters == nil || quote.ReplyParameters.MessageID != id || d.CardID != cardID || d.Content != "Body" || !d.Preview {
		t.Fatal("a review-only source could not be located without changing its draft")
	}
	h.api.mu.Lock()
	h.api.replyError = "message to be replied not found"
	h.api.mu.Unlock()
	h.click(action)
	d = h.active()
	if len(h.api.live()) != 1 || d.SourceReplyID != 0 || !d.Preview || d.Content != "Body" || d.CardID != cardID || d.Notice != fmt.Sprintf("Source unavailable · /remove %d", id) {
		t.Fatal("missing source escaped the existing preview or created an orphan reply")
	}
	h.send(fmt.Sprintf("/remove %d", id))
	if len(h.active().MessageIssues) != 0 || h.active().Content != "Body" || len(h.api.live()) != 1 {
		t.Fatal("the missing-source recovery action did not work")
	}
}

func TestSourceQuoteCleanupFailureDoesNotSpamOrBlockWriting(t *testing.T) {
	h := newHarness(t)
	d := h.ready("Body")
	action := fmt.Sprintf("source-%d", h.messageID)
	h.click(action)
	quoteID := h.active().SourceReplyID
	sent := h.api.count("sendMessage", false)
	h.api.mu.Lock()
	h.api.deleteRejected = true
	h.api.mu.Unlock()
	h.click(action)
	if h.api.count("sendMessage", false) != sent || h.active().SourceReplyID != quoteID || !strings.Contains(h.active().Notice, "previous source reply") {
		t.Fatal("cleanup failure accumulated another quote or lacked inline feedback")
	}
	h.send("Addition")
	if h.active().Content != "Body\n\nAddition" || h.active().CardID != d.CardID {
		t.Fatal("a quote cleanup failure blocked authoring")
	}
	h.api.mu.Lock()
	h.api.deleteRejected = false
	h.api.mu.Unlock()
	h.send("More")
	if h.active().SourceReplyID != 0 || len(h.api.live()) != 1 {
		t.Fatal("cleanup did not recover on the next card update")
	}
	h.click(action)
	h.send("/cancel")
	if len(h.api.live()) != 0 {
		t.Fatal("cancelling a draft left its quote or card behind")
	}
}

func TestRepositoryOnlyArticleHasNoOriginalMessageButton(t *testing.T) {
	h := newHarness(t)
	h.app.Repository = &fakeRepository{entries: []post.Article{exampleArticle(t, 268, time.Date(2020, 1, 2, 0, 0, 0, 0, time.UTC))}}
	h.send("/edit 268")
	_, markup := card(h.active())
	for _, row := range markup.InlineKeyboard {
		for _, button := range row {
			if button.Text == "Original message" {
				t.Fatal("article with no recorded source advertised an original message")
			}
		}
	}
	d := h.active()
	sent := h.api.count("sendMessage", false)
	h.callback(keyboard(d, "Forged source", "source-999").InlineKeyboard[0][0].CallbackData)
	if h.api.count("sendMessage", false) != sent || len(h.api.live()) != 1 || !strings.Contains(h.active().Notice, "no longer linked") {
		t.Fatal("a source not belonging to the article was quoted")
	}
}
