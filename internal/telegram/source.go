package telegram

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"newspaperbot/internal/post"
)

func sourceButtons(d post.Draft, markup *models.InlineKeyboardMarkup) {
	for _, source := range d.Sources {
		if !source.Full || source.MessageID <= 0 || d.View != "" && d.View != "preview" {
			continue
		}
		button := keyboard(d, "Original message", fmt.Sprintf("source-%d", source.MessageID)).InlineKeyboard[0][0]
		placed := false
		for i, row := range markup.InlineKeyboard {
			if slices.ContainsFunc(row, func(b models.InlineKeyboardButton) bool { return strings.HasSuffix(b.CallbackData, ":cancel") }) {
				markup.InlineKeyboard[i] = append([]models.InlineKeyboardButton{button}, row...)
				placed = true
				break
			}
		}
		if !placed {
			markup.InlineKeyboard = append(markup.InlineKeyboard, []models.InlineKeyboardButton{button})
		}
		break
	}
	for i, issue := range d.MediaIssues() {
		if i%2 == 0 {
			markup.InlineKeyboard = append(markup.InlineKeyboard, nil)
		}
		row := len(markup.InlineKeyboard) - 1
		button := keyboard(d, fmt.Sprintf("Source %d", i+1), fmt.Sprintf("source-%d", issue.MessageID)).InlineKeyboard[0][0]
		markup.InlineKeyboard[row] = append(markup.InlineKeyboard[row], button)
	}
}

func (a *App) showSource(ctx context.Context, b *bot.Bot, d *post.Draft, id int) error {
	linked := slices.ContainsFunc(d.Sources, func(s post.Source) bool { return s.MessageID == id }) ||
		slices.ContainsFunc(d.MediaIssues(), func(s post.MessageIssue) bool { return s.MessageID == id }) ||
		d.ReplacementSource != nil && d.ReplacementSource.MessageID == id
	if id <= 0 || !linked {
		return a.notice(ctx, b, d, "Source no longer linked · use the current controls")
	}
	if err := a.clearSourceReply(ctx, b, d); err != nil {
		a.logError(b, "source reply cleanup", err)
		return a.notice(ctx, b, d, "Could not clear the previous source reply · delete it and retry")
	}
	message, err := b.SendMessage(ctx, &bot.SendMessageParams{
		ChatID: a.OwnerID, Text: "<i>Tap the quote to open the source.</i>", ParseMode: models.ParseModeHTML,
		ReplyParameters: &models.ReplyParameters{MessageID: id}, DisableNotification: true,
	})
	if err != nil {
		a.logError(b, "source reply", err)
		if errors.Is(err, bot.ErrorBadRequest) && strings.Contains(strings.ToLower(err.Error()), "message to be replied") {
			text := "Source unavailable"
			if !d.Locked() {
				if slices.ContainsFunc(d.Sources, func(s post.Source) bool { return s.MessageID == id && s.Full }) {
					text += " · use /replace"
				} else {
					text += fmt.Sprintf(" · /remove %d", id)
				}
			} else if d.Revision == nil && d.Number > 0 {
				text += fmt.Sprintf(" · use /edit %d", d.Number)
			}
			return a.notice(ctx, b, d, text)
		}
		return a.notice(ctx, b, d, "Could not open the source · try again")
	}
	d.SourceReplyID, d.SourceReplyToID = message.ID, id
	if err := a.Store.SaveCard(ctx, d); err != nil {
		a.deleteOwnerMessage(ctx, b, message.ID, "untracked source reply")
		d.SourceReplyID, d.SourceReplyToID = 0, 0
		return err
	}
	return nil
}

func (a *App) clearSourceReply(ctx context.Context, b *bot.Bot, d *post.Draft) error {
	if d.SourceReplyID == 0 {
		return nil
	}
	_, err := b.DeleteMessage(ctx, &bot.DeleteMessageParams{ChatID: a.OwnerID, MessageID: d.SourceReplyID})
	missing := errors.Is(err, bot.ErrorBadRequest) && strings.Contains(strings.ToLower(err.Error()), "message to delete not found")
	if err != nil && !missing {
		return err
	}
	d.SourceReplyID, d.SourceReplyToID = 0, 0
	return a.Store.SaveCard(ctx, d)
}
