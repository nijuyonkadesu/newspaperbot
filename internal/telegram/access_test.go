package telegram

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	"github.com/go-telegram/bot/models"
)

func TestUnauthorizedUpdatesStaySilentEvenWhenCommandsWouldFail(t *testing.T) {
	h := newHarness(t)
	draft := h.ready("Owner's content")
	before := len(h.api.snapshot())
	previewCtx, cancel := context.WithCancel(t.Context())
	defer cancel()
	h.app.ownerPreview.Store(&previewJob{cancel: cancel})
	// No database means an unauthorized request cannot even enter a command's
	// error path, much less send an error reply or touch the owner's content.
	db := h.app.Store
	h.app.Store = nil
	defer func() { h.app.Store = db }()
	commands := []string{"/start", "/help", "/newpost", "/taxonomy", "/drafts", "/resume invalid", "/posts", "/edit invalid", "/save", "/publish", "/replace", "/undo", "/remove invalid", "/download", "/download 1000", "/download invalid", "/cancel", "/delete invalid", "/channels", "/setchannel invalid", "/unsetchannel", "/unknown", "Edited content"}
	chats := []struct {
		name string
		from *models.User
		chat models.Chat
	}{
		{"stranger DM", &models.User{ID: 99}, models.Chat{ID: 99, Type: models.ChatTypePrivate}},
		{"wrong sender", &models.User{ID: 99}, models.Chat{ID: 42, Type: models.ChatTypePrivate}},
		{"wrong DM", &models.User{ID: 42}, models.Chat{ID: 99, Type: models.ChatTypePrivate}},
		{"owner in group", &models.User{ID: 42}, models.Chat{ID: -1001, Type: models.ChatTypeSupergroup}},
		{"missing sender", nil, models.Chat{ID: 42, Type: models.ChatTypePrivate}},
	}
	for _, chat := range chats {
		for _, command := range commands {
			for _, edited := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/edited=%t", chat.name, command, edited), func(t *testing.T) {
					message := &models.Message{ID: draft.Sources[0].MessageID, From: chat.from, Chat: chat.chat, Text: command,
						ReplyToMessage: &models.Message{ID: draft.CardID},
						Entities:       []models.MessageEntity{{Type: models.MessageEntityTypeBotCommand, Offset: 0, Length: len(command)}},
					}
					update := &models.Update{Message: message}
					if edited {
						update.Message, update.EditedMessage = nil, message
					}
					h.app.Handle(context.Background(), h.bot, update)
					if len(h.api.snapshot()) != before {
						t.Fatal("unauthorized request caused a Telegram API call")
					}
				})
			}
		}
	}
	for _, query := range []*models.CallbackQuery{
		{From: models.User{ID: 99}, Data: keyboard(draft, "Download", "download").InlineKeyboard[0][0].CallbackData, Message: models.MaybeInaccessibleMessage{Message: &models.Message{ID: draft.CardID, Chat: models.Chat{ID: 42, Type: models.ChatTypePrivate}}}},
		{From: models.User{ID: 99}, Data: "preview:1000", Message: models.MaybeInaccessibleMessage{Message: &models.Message{ID: draft.CardID, Chat: models.Chat{ID: 99, Type: models.ChatTypePrivate}}}},
		{From: models.User{ID: 42}, Data: "invalid", Message: models.MaybeInaccessibleMessage{Message: &models.Message{Chat: models.Chat{ID: -1001, Type: models.ChatTypeChannel}}}},
		{From: models.User{ID: 99}, InlineMessageID: "inline", Data: "article:1000"},
		{From: models.User{ID: 42}, Data: "invalid"},
	} {
		h.app.Handle(context.Background(), h.bot, &models.Update{CallbackQuery: query})
	}
	h.app.Handle(context.Background(), h.bot, &models.Update{ChannelPost: &models.Message{Text: "/download 1000"}})
	if len(h.api.snapshot()) != before {
		t.Fatal("unauthorized callback received an answer or triggered an error reply")
	}
	if previewCtx.Err() != nil {
		t.Fatal("unauthorized request cancelled the owner's preview")
	}
	h.app.ownerPreview.Store(nil)
	h.app.Store = db
	if !reflect.DeepEqual(h.active(), draft) {
		t.Fatal("unauthorized request changed the owner's draft")
	}
	h.send("Owner's addition")
	if h.active().Content != "Owner's content\n\nOwner's addition" {
		t.Fatal("owner was denied after unauthorized requests")
	}
}
