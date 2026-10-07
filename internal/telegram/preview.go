package telegram

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"newspaperbot/internal/linkpreview"
)

// Metadata and generated labels are literal text inside rich Markdown, not HTML.
// Escape backslashes first; protect tags/entities without introducing new ones.
func escapeRichText(text string) string {
	text = strings.ReplaceAll(text, "\\", "\\\\")
	return strings.NewReplacer("<", "\\<", "&", "\\&").Replace(bot.EscapeMarkdown(text))
}

// writeText renders Markdown first; URL work and media upload run afterward.
func (a *App) writeText(ctx context.Context, b *bot.Bot, id int, text string, markup *models.InlineKeyboardMarkup, rich *models.InputRichMessage) (*models.Message, error) {
	a.cancelOwnerPreview()
	return a.writePreview(ctx, b, a.OwnerID, id, text, markup, rich, true)
}

func (a *App) writeMessage(ctx context.Context, b *bot.Bot, chatID int64, id int, text string, markup *models.InlineKeyboardMarkup, rich *models.InputRichMessage) (*models.Message, error) {
	disabled := true
	options := &models.LinkPreviewOptions{IsDisabled: &disabled}
	if id != 0 {
		params := &bot.EditMessageTextParams{ChatID: chatID, MessageID: id, Text: text, ParseMode: models.ParseModeHTML, ReplyMarkup: markup, LinkPreviewOptions: options}
		if rich != nil {
			params.Text, params.ParseMode, params.RichMessage = "", "", rich
		}
		return b.EditMessageText(ctx, params)
	}
	if rich != nil {
		return b.SendRichMessage(ctx, &bot.SendRichMessageParams{ChatID: chatID, RichMessage: *rich, ReplyMarkup: markup, DisableNotification: chatID == a.OwnerID})
	}
	return b.SendMessage(ctx, &bot.SendMessageParams{ChatID: chatID, Text: text, ParseMode: models.ParseModeHTML, ReplyMarkup: markup, DisableNotification: true, LinkPreviewOptions: options})
}

func (a *App) writePreview(ctx context.Context, b *bot.Bot, chatID int64, id int, text string, markup *models.InlineKeyboardMarkup, rich *models.InputRichMessage, footer bool) (*models.Message, error) {
	target := previewTarget{chatID, id}
	a.cancelPreview(target)
	if rich == nil || a.Previews == nil || rich.Markdown == "" || len(rich.Media) != 0 {
		return a.writeMessage(ctx, b, chatID, id, text, markup, rich)
	}
	message, err := a.writeMessage(ctx, b, chatID, id, text, markup, rich)
	if err == nil || unchangedMessage(err) {
		if message != nil {
			target.id = message.ID
		}
		if card, known := a.Previews.Cached(rich.Markdown); !known || card.Title != "" {
			a.queuePreview(ctx, b, target, markup, *rich, rich.Markdown, footer)
		}
	}
	return message, err
}

// Both author cards and destination posts use the same rich content and fallbacks.
func (a *App) writeLinkCard(ctx context.Context, b *bot.Bot, target previewTarget, markup *models.InlineKeyboardMarkup, rich *models.InputRichMessage, card linkpreview.Card, footer bool) (*models.Message, error) {
	imageRejected := false
	if card.Title != "" {
		for _, withImage := range []bool{true, false} {
			if withImage && len(card.Image) == 0 {
				continue
			}
			content := *rich
			content.Markdown = appendLinkCard(rich.Markdown, card, withImage, footer)
			if content.Markdown == rich.Markdown || utf8.RuneCountInString(content.Markdown) > 32768 {
				continue
			}
			if withImage {
				content.Media = []models.InputRichMessageMedia{{ID: "newspaperbot_preview", Media: &models.InputMediaPhoto{
					Media: "attach://newspaperbot-preview." + card.ImageFormat, MediaAttachment: bytes.NewReader(card.Image),
				}}}
			}
			message, err := a.writeMessage(ctx, b, target.chat, target.id, "", markup, &content)
			if err == nil || unchangedMessage(err) {
				if imageRejected {
					a.Previews.OmitImage(card.URL)
				}
				return message, err
			}
			if !errors.Is(err, bot.ErrorBadRequest) || missingCard(err) {
				return message, err
			}
			imageRejected = withImage
		}
	}
	// The original Markdown is already visible. Optional failures keep it intact.
	return nil, nil
}

// Owner cards insert before their generated footer; destination cards append
// after the entire article. Dividers inside the source body aren't footers.
func appendLinkCard(markdown string, card linkpreview.Card, withImage, footer bool) string {
	i := len(markdown)
	if footer {
		i = strings.LastIndex(markdown, "\n\n---\n\n")
		if i < 0 {
			return markdown
		}
	}
	destination := strings.NewReplacer("(", "%28", ")", "%29", "<", "%3C", ">", "%3E", "\"", "%22", "'", "%27", "\\", "%5C", "`", "%60").Replace(card.URL)
	block := "\n\n---\n\n"
	if withImage {
		block += "![](tg://photo?id=newspaperbot_preview)\n\n"
	}
	block += "**[" + escapeRichText(card.Title) + "](" + destination + ")**"
	if card.Description != "" {
		block += "\n\n" + escapeRichText(card.Description)
	}
	return markdown[:i] + block + markdown[i:]
}
