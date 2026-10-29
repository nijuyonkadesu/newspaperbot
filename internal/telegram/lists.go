package telegram

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

const listItemsPerPage = 8

// Commands bring lists to the bottom; paging and previews edit them in place.
func (a *App) writeList(ctx context.Context, b *bot.Bot, key, text string, markup *models.InlineKeyboardMarkup, replace bool) error {
	id, err := a.Store.Setting(ctx, key+"_message")
	if err != nil {
		return err
	}
	if id != 0 && !replace {
		_, err := a.writeText(ctx, b, int(id), text, markup, nil)
		if err == nil || unchangedMessage(err) {
			return nil
		}
		if !missingCard(err) {
			return err
		}
	}
	message, err := a.writeText(ctx, b, 0, text, markup, nil)
	if err != nil {
		return err
	}
	if err := a.Store.SetSetting(ctx, key+"_message", int64(message.ID)); err != nil {
		a.deleteCard(ctx, b, message.ID)
		return err
	}
	if id != 0 {
		a.deleteCard(ctx, b, int(id))
	}
	return nil
}

func (a *App) listNumberReply(ctx context.Context, b *bot.Bot, m *models.Message, key, item string, show func(context.Context, *bot.Bot, int64, string, bool) error) (bool, error) {
	text := strings.TrimSpace(m.Text)
	if m.ReplyToMessage == nil || strings.HasPrefix(text, "/") {
		return false, nil
	}
	id, err := a.Store.Setting(ctx, key+"_message")
	if err != nil {
		return true, err
	}
	if id == 0 || int64(m.ReplyToMessage.ID) != id {
		return false, nil
	}
	if text == "" {
		return true, nil
	}
	anchor, parseErr := strconv.ParseInt(text, 10, 64)
	notice := ""
	if parseErr != nil || anchor <= 0 {
		anchor, err = a.Store.Setting(ctx, key+"_anchor")
		if err != nil {
			return true, err
		}
		notice = fmt.Sprintf("Reply with a positive %s number.", item)
	}
	if err := show(ctx, b, anchor, notice, false); err != nil {
		return true, err
	}
	a.deleteOwnerMessage(ctx, b, m.ID, item+" list reply")
	return true, nil
}
