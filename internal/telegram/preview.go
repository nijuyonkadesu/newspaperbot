package telegram

import (
	"context"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

// Cards and article previews share the original rich-message transport.
func (a *App) writeText(ctx context.Context, b *bot.Bot, id int, text string, markup *models.InlineKeyboardMarkup, rich *models.InputRichMessage) (*models.Message, error) {
	disabled := true
	options := &models.LinkPreviewOptions{IsDisabled: &disabled}
	if id != 0 {
		params := &bot.EditMessageTextParams{ChatID: a.OwnerID, MessageID: id, Text: text, ParseMode: models.ParseModeHTML, ReplyMarkup: markup, LinkPreviewOptions: options}
		if rich != nil {
			params.Text, params.ParseMode, params.RichMessage = "", "", rich
		}
		return b.EditMessageText(ctx, params)
	}
	if rich != nil {
		return b.SendRichMessage(ctx, &bot.SendRichMessageParams{ChatID: a.OwnerID, RichMessage: *rich, ReplyMarkup: markup, DisableNotification: true})
	}
	return b.SendMessage(ctx, &bot.SendMessageParams{ChatID: a.OwnerID, Text: text, ParseMode: models.ParseModeHTML, ReplyMarkup: markup, DisableNotification: true, LinkPreviewOptions: options})
}
