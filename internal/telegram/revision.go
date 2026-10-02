package telegram

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"newspaperbot/internal/post"
)

func unchangedMessage(err error) bool {
	return errors.Is(err, bot.ErrorBadRequest) && strings.Contains(strings.ToLower(err.Error()), "message is not modified")
}

func (a *App) saveChanges(ctx context.Context, b *bot.Bot, d *post.Draft) error {
	if d.Revision == nil {
		return a.notice(ctx, b, d, "Use /edit <article number> to change a live article.")
	}
	if d.Revision.Applied {
		return a.finishRevision(ctx, b, d)
	}
	if d.Revision.Conflict {
		return a.notice(ctx, b, d, "Article changed on main · download your changes or discard and reload")
	}
	if a.Repository == nil {
		return a.notice(ctx, b, d, "Restore repository configuration to save these changes.")
	}
	if err := d.ValidatePortfolio(); err != nil {
		return a.notice(ctx, b, d, err.Error())
	}
	if d.GitOperation == "" {
		data, err := d.Markdown()
		if err != nil {
			return err
		}
		if string(data) == d.Revision.Original {
			return a.notice(ctx, b, d, "No changes to save.")
		}
	}
	queued, err := a.Store.Queue(ctx, d.ID, d.ChannelID)
	if err != nil {
		return err
	}
	*d = queued
	return a.render(ctx, b, d)
}

// Channel edits are idempotent: an interrupted edit can be retried without
// creating messages. Checkpoint each part of summary/document deliveries.
func (a *App) finishRevision(ctx context.Context, b *bot.Bot, d *post.Draft) error {
	r := d.Revision
	if d.ChannelID != 0 {
		for _, summary := range []bool{true, false} {
			id, done := d.ContentMessageID, r.ContentDone
			if summary {
				id, done = d.SummaryMessageID, r.SummaryDone
			}
			if done || id == 0 {
				continue
			}
			var err error
			if summary {
				_, err = b.EditMessageText(ctx, &bot.EditMessageTextParams{ChatID: d.ChannelID, MessageID: id, Text: d.Title + "\n\n" + d.Summary})
			} else {
				document := d.DocumentMode || utf8.RuneCountInString(d.RichMarkdown()) > 32768
				if !document {
					_, err = b.EditMessageText(ctx, &bot.EditMessageTextParams{ChatID: d.ChannelID, MessageID: id, RichMessage: &models.InputRichMessage{Markdown: d.RichMarkdown()}})
					document = errors.Is(err, bot.ErrorBadRequest) && !missingCard(err) && !unchangedMessage(err)
				}
				if document {
					data, encodeErr := d.Markdown()
					if encodeErr != nil {
						return encodeErr
					}
					_, err = b.EditMessageMedia(ctx, &bot.EditMessageMediaParams{ChatID: d.ChannelID, MessageID: id, Media: &models.InputMediaDocument{Media: "attach://" + filepath.Base(d.Filename), MediaAttachment: bytes.NewReader(data)}})
					if err == nil || unchangedMessage(err) {
						d.DocumentMode = true
					}
				}
			}
			if missingCard(err) || errors.Is(err, bot.ErrorNotFound) {
				r.Missing = true
			} else if err != nil && !unchangedMessage(err) {
				a.logError(b, "article channel edit", err)
				return a.notice(ctx, b, d, "Repository updated · channel update pending")
			}
			if summary {
				r.SummaryDone = true
			} else {
				r.ContentDone = true
			}
			if err := a.Store.Save(ctx, d); err != nil {
				return err
			}
		}
	}
	d.Notice = "Repository updated"
	if d.ChannelID == 0 || d.SummaryMessageID == 0 && d.ContentMessageID == 0 {
		d.Notice += " · no linked channel message"
	} else if r.Missing {
		d.Notice += " · channel message unavailable"
	} else {
		d.Notice += " · channel updated"
	}
	d.Revision, d.View, d.Delivery = nil, "", "sent"
	if err := a.Store.Save(ctx, d); err != nil {
		return err
	}
	return a.render(ctx, b, d)
}
