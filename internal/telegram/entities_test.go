package telegram

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/go-telegram/bot/models"
	"newspaperbot/internal/post"
)

func TestEntityMarkdownPreservesTextLinksAndUTF16Offsets(t *testing.T) {
	text := "🙂 See Linux and underlined"
	got, incomplete := entityMarkdown(text, []models.MessageEntity{
		{Type: models.MessageEntityTypeTextLink, Offset: 7, Length: 5, URL: "https://linux.example/a)b"},
		{Type: models.MessageEntityTypeBold, Offset: 7, Length: 5},
		{Type: models.MessageEntityTypeUnderline, Offset: 17, Length: 10},
		{Type: models.MessageEntityTypeItalic, Offset: 1, Length: 1}, // Inside the emoji's surrogate pair.
	})
	if want := "🙂 See [**Linux**](https://linux.example/a\\)b) and underlined"; got != want || !incomplete {
		t.Fatalf("conversion = %q, incomplete = %v; want %q, true", got, incomplete, want)
	}
}

func TestReviewStatusIsCompactAndActionable(t *testing.T) {
	issues := []post.MessageIssue{{MessageID: 42, Reason: "formatting kept as text"}, {MessageID: 43, Reason: "rich content skipped"}}
	markdown := messageIssuesMarkdown(issues)
	if strings.Count(markdown, "---") != 0 || !strings.Contains(markdown, "**Review**\n\n1. formatting kept as text · `/remove 42`\n2. rich content skipped · `/remove 43`") {
		t.Fatalf("noisy or incomplete review Markdown: %q", markdown)
	}
	html := messageIssuesHTML(issues[:1])
	if html != "\n\n<b>Review</b>\n1. formatting kept as text · <code>/remove 42</code>" {
		t.Fatalf("noisy or incomplete review HTML: %q", html)
	}
	if statusMarkdown("Source unavailable") != "\n\n**Status** · Source unavailable" {
		t.Fatal("preview status is not compact")
	}
}

func TestForwardedEntitiesUpdateTheSamePreviewCard(t *testing.T) {
	h := newHarness(t)
	h.ready("Body")
	h.click("preview")
	cardID := h.active().CardID
	sends, deletes := h.api.count("sendMessage", false), h.api.count("deleteMessage", false)

	h.messageID++
	messageID := h.messageID
	h.app.Handle(context.Background(), h.bot, &models.Update{ID: int64(messageID), Message: &models.Message{
		ID: messageID, From: &models.User{ID: 42}, Chat: models.Chat{ID: 42, Type: models.ChatTypePrivate},
		ForwardOrigin: &models.MessageOrigin{Type: models.MessageOriginTypeUser},
		Text:          "🙂 See Linux and underlined",
		Entities: []models.MessageEntity{
			{Type: models.MessageEntityTypeTextLink, Offset: 7, Length: 5, URL: "https://linux.example"},
			{Type: models.MessageEntityTypeUnderline, Offset: 17, Length: 10},
		},
	}})

	d := h.active()
	if d.Content != "Body\n\n🙂 See [Linux](https://linux.example) and underlined" || len(d.MessageIssues) != 1 {
		t.Fatalf("forwarded message was not preserved: content=%q issues=%+v", d.Content, d.MessageIssues)
	}
	live := h.api.live()
	_, markup := card(d)
	button := markup.InlineKeyboard[len(markup.InlineKeyboard)-1][0]
	if !d.Preview || d.View != "preview" || d.CardID != cardID || len(live) != 1 || button.Text != "Source 1" || !strings.HasSuffix(button.CallbackData, fmt.Sprintf(":source-%d", messageID)) {
		t.Fatal("forwarded source replaced the card, lost preview mode, or omitted its review action")
	}
	if h.api.count("sendMessage", false) != sends || h.api.count("deleteMessage", false) != deletes {
		t.Fatal("forwarded source sent or deleted a draft card")
	}

	h.messageID++
	h.app.Handle(context.Background(), h.bot, &models.Update{ID: int64(h.messageID), EditedMessage: &models.Message{
		ID: messageID, EditDate: 1, From: &models.User{ID: 42}, Chat: models.Chat{ID: 42, Type: models.ChatTypePrivate},
		Text: "Read Linux", Entities: []models.MessageEntity{{Type: models.MessageEntityTypeTextLink, Offset: 5, Length: 5, URL: "https://linux.example"}},
	}})
	d = h.active()
	if d.Content != "Body\n\nRead [Linux](https://linux.example)" || len(d.MessageIssues) != 0 || d.CardID != cardID || !d.Preview {
		t.Fatal("editing a forwarded source did not repair it in place")
	}
}

func TestRichMessagesAreReferencedAndMediaCaptionsAreRetained(t *testing.T) {
	h := newHarness(t)
	h.ready("Body")
	h.click("preview")
	cardID := h.active().CardID

	h.messageID++
	richID := h.messageID
	h.app.Handle(context.Background(), h.bot, &models.Update{ID: int64(richID), Message: &models.Message{
		ID: richID, From: &models.User{ID: 42}, Chat: models.Chat{ID: 42, Type: models.ChatTypePrivate},
		ForwardOrigin: &models.MessageOrigin{Type: models.MessageOriginTypeChannel}, RichMessage: &models.RichMessage{},
	}})
	d := h.active()
	if d.Content != "Body" || len(d.MessageIssues) != 1 || d.MessageIssues[0].MessageID != richID || d.CardID != cardID || !d.Preview {
		t.Fatalf("rich message handling changed content or state: %+v", d)
	}
	_, markup := card(d)
	if button := markup.InlineKeyboard[len(markup.InlineKeyboard)-1][0]; button.Text != "Source 1" || !strings.HasSuffix(button.CallbackData, fmt.Sprintf(":source-%d", richID)) {
		t.Fatal("rich message lacked a source action on the preview")
	}

	calls := len(h.api.snapshot())
	h.messageID++
	h.app.Handle(context.Background(), h.bot, &models.Update{ID: int64(h.messageID), Message: &models.Message{
		ID: h.messageID, From: &models.User{ID: 42}, Chat: models.Chat{ID: 42, Type: models.ChatTypePrivate},
		Caption: "Picture caption", CaptionEntities: []models.MessageEntity{{Type: models.MessageEntityTypeBold, Offset: 0, Length: 7}},
		Photo: []models.PhotoSize{{FileID: "photo", FileUniqueID: "unique-photo"}}, MediaGroupID: "album",
	}})
	d = h.active()
	imageBody := "Body\n\n![](" + post.ImageURLDir + post.ImageAsset("unique-photo", "jpg") + ")\n\n**Picture** caption"
	if d.Content != imageBody || len(d.MessageIssues) != 1 || len(h.api.snapshot()) != calls+1 || d.CardID != cardID || !d.Preview {
		t.Fatal("media caption was lost or rich-source review/card state changed")
	}

	h.messageID++
	h.app.Handle(context.Background(), h.bot, &models.Update{ID: int64(h.messageID), EditedMessage: &models.Message{
		ID: richID, EditDate: 1, From: &models.User{ID: 42}, Chat: models.Chat{ID: 42, Type: models.ChatTypePrivate}, Text: "Recovered text",
	}})
	d = h.active()
	if d.Content != imageBody+"\n\nRecovered text" || len(d.MessageIssues) != 0 || d.CardID != cardID || !d.Preview {
		t.Fatal("a rich source edited into text was not recovered in place")
	}
}

func TestRemoveCommandDropsRepliedOrDeletedSource(t *testing.T) {
	h := newHarness(t)
	h.ready("Body")
	h.click("preview")
	cardID := h.active().CardID

	h.messageID++
	firstID := h.messageID
	h.app.Handle(context.Background(), h.bot, &models.Update{ID: int64(firstID), Message: &models.Message{
		ID: firstID, From: &models.User{ID: 42}, Chat: models.Chat{ID: 42, Type: models.ChatTypePrivate}, Text: "Needs review",
		ForwardOrigin: &models.MessageOrigin{Type: models.MessageOriginTypeUser},
		Entities:      []models.MessageEntity{{Type: models.MessageEntityTypeUnderline, Offset: 0, Length: 5}},
	}})
	h.send("Keep this")
	h.replyTo(firstID, "/remove")
	firstCommandID := h.messageID
	d := h.active()
	if d.Content != "Body\n\nKeep this" || len(d.MessageIssues) != 0 || d.CardID != cardID || !d.Preview {
		t.Fatalf("reply removal did not rebuild the same preview: %+v", d)
	}
	assertDeleted := func(ids ...int) {
		t.Helper()
		found := map[int]bool{}
		for _, call := range h.api.snapshot() {
			if call.Method == "deleteMessage" {
				found[call.MessageID] = true
			}
		}
		for _, id := range ids {
			if !found[id] {
				t.Fatalf("message %d was not deleted", id)
			}
		}
	}
	assertDeleted(firstID, firstCommandID)

	h.send("Delete in Telegram next")
	deletedID := h.messageID
	h.send(fmt.Sprintf("/remove %d", deletedID))
	secondCommandID := h.messageID
	d = h.active()
	if d.Content != "Body\n\nKeep this" || d.CardID != cardID || !d.Preview {
		t.Fatal("message-ID removal did not handle an already deleted source")
	}
	assertDeleted(deletedID, secondCommandID)
}

func TestRemoveErrorsStayOnTheDraftCard(t *testing.T) {
	h := newHarness(t)
	h.ready("Body")
	rootID := h.messageID
	h.click("preview")
	cardID := h.active().CardID
	sends := h.api.count("sendMessage", false)

	h.replyTo(rootID, "/remove")
	d := h.active()
	if d.CardID != cardID || !d.Preview || !strings.Contains(h.api.live()[0].RichMarkdown, "**Status** · Original post") || h.api.count("sendMessage", false) != sends {
		t.Fatal("remove error escaped the existing preview card")
	}
	if h.api.count("deleteMessage", false) != 1 {
		t.Fatal("failed removal did not delete only its command")
	}

	h.send("/remove")
	d = h.active()
	if d.CardID != cardID || !strings.Contains(h.api.live()[0].RichMarkdown, "**Status** · Reply with /remove") || h.api.count("sendMessage", false) != sends {
		t.Fatal("remove usage created a new message or disappeared from preview")
	}
	if h.api.count("deleteMessage", false) != 2 {
		t.Fatal("usage cleanup did not delete its command")
	}
}
