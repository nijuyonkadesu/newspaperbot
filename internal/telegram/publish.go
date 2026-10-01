package telegram

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"unicode/utf8"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"tgblogbot/internal/post"
)

func sendDocument(ctx context.Context, b *bot.Bot, chatID int64, d post.Draft) (*models.Message, error) {
	data, err := d.Markdown()
	if err != nil {
		return nil, err
	}
	name := fmt.Sprintf("draft-%d.md", d.ID)
	if d.Number != 0 {
		name = fmt.Sprintf("%d.md", d.Number)
		if d.Portfolio && d.Filename != "" {
			name = filepath.Base(d.Filename)
		}
	}
	return b.SendDocument(ctx, &bot.SendDocumentParams{ChatID: chatID, Document: &models.InputFileUpload{Filename: name, Data: bytes.NewReader(data)}})
}

func (a *App) publish(ctx context.Context, b *bot.Bot, d *post.Draft, retry bool) error {
	if a.Repository == nil && d.GitOperation != "" && d.Number == 0 {
		return a.notice(ctx, b, d, "Repository publishing is not configured. Restore its configuration to continue this publication.")
	}
	if a.Repository != nil && d.Number == 0 {
		return a.queuePublication(ctx, b, d)
	}
	if err := d.Validate(); err != nil {
		return a.notice(ctx, b, d, err.Error())
	}
	if retry && d.Delivery != "uncertain" {
		return a.notice(ctx, b, d, "There is no uncertain delivery to retry.")
	}
	if d.Delivery == "uncertain" && !retry {
		return a.prompt(ctx, b, *d)
	}
	if d.Number == 0 {
		catalog, err := a.Metadata.Load(ctx)
		if err != nil {
			return fmt.Errorf("publication metadata: %w", err)
		}
		d.Categories, d.AvailableTags = catalog.Categories, catalog.Tags
		valid := slices.Contains(catalog.Categories, d.Category)
		for _, tag := range d.Tags {
			valid = valid && slices.Contains(catalog.Tags, tag)
		}
		if err := a.Store.Save(ctx, d); err != nil {
			return err
		}
		if !valid {
			d.View = "options"
			return a.notice(ctx, b, d, "Categories or tags changed. Choose current values under Category and Tags.")
		}
		channel, err := a.Store.Setting(ctx, "channel")
		if err != nil {
			return err
		}
		if channel != 0 {
			if err := a.checkChannel(ctx, b, channel); err != nil {
				return a.notice(ctx, b, d, err.Error())
			}
		}
		localNumber, err := post.HighestNumber(a.OutputDir)
		if err != nil {
			return err
		}
		*d, err = a.Store.Reserve(ctx, d.ID, max(catalog.LastNumber, localNumber), channel, a.OutputDir)
		if err != nil {
			return err
		}
	}
	if !d.Exported {
		data, err := d.Markdown()
		if err != nil {
			return err
		}
		if err := a.WriteFile(d.Filename, data); err != nil {
			return err
		}
		d.Exported = true
		if err := a.Store.Save(ctx, d); err != nil {
			return err
		}
	}
	if d.ChannelID != 0 && d.Delivery != "sent" {
		if err := a.checkChannel(ctx, b, d.ChannelID); err != nil {
			return a.notice(ctx, b, d, "Markdown saved. "+err.Error()+". Restore channel access and continue publishing.")
		}
		if err := a.deliver(ctx, b, d); err != nil {
			return err
		}
		if d.Delivery != "sent" {
			return nil
		}
	}
	d.View, d.Notice = "", ""
	if err := a.Store.Save(ctx, d); err != nil {
		return err
	}
	return a.prompt(ctx, b, *d)
}

// Each send is marked uncertain before the network call. A crash, timeout, or
// failed DB write then requires an explicit retry after checking the channel.
func (a *App) deliver(ctx context.Context, b *bot.Bot, d *post.Draft) error {
	if utf8.RuneCountInString(d.RichMarkdown()) > 32768 {
		d.DocumentMode = true
	}
	if !d.DocumentMode {
		if err := a.beforeSend(ctx, d); err != nil {
			return err
		}
		message, err := b.SendRichMessage(ctx, &bot.SendRichMessageParams{ChatID: d.ChannelID, RichMessage: models.InputRichMessage{Markdown: d.RichMarkdown()}})
		if errors.Is(err, bot.ErrorBadRequest) {
			d.DocumentMode = true
			d.Delivery = "pending"
			if err := a.Store.Save(ctx, d); err != nil {
				return err
			}
		} else {
			if err != nil {
				return a.deliveryError(ctx, b, d, err)
			}
			d.ContentMessageID = message.ID
			d.Delivery = "sent"
			return a.Store.Save(ctx, d)
		}
	}
	if d.SummaryMessageID == 0 {
		if err := a.beforeSend(ctx, d); err != nil {
			return err
		}
		message, err := b.SendMessage(ctx, &bot.SendMessageParams{ChatID: d.ChannelID, Text: d.Title + "\n\n" + d.Summary})
		if err != nil {
			return a.deliveryError(ctx, b, d, err)
		}
		d.SummaryMessageID = message.ID
		d.Delivery = "pending"
		if err := a.Store.Save(ctx, d); err != nil {
			return err
		}
	}
	if err := a.beforeSend(ctx, d); err != nil {
		return err
	}
	message, err := sendDocument(ctx, b, d.ChannelID, *d)
	if err != nil {
		return a.deliveryError(ctx, b, d, err)
	}
	d.ContentMessageID = message.ID
	d.Delivery = "sent"
	return a.Store.Save(ctx, d)
}

func (a *App) beforeSend(ctx context.Context, d *post.Draft) error {
	d.Delivery = "uncertain"
	return a.Store.Save(ctx, d)
}

func (a *App) deliveryError(ctx context.Context, b *bot.Bot, d *post.Draft, err error) error {
	if errors.Is(err, bot.ErrorBadRequest) || errors.Is(err, bot.ErrorForbidden) || errors.Is(err, bot.ErrorUnauthorized) || errors.Is(err, bot.ErrorNotFound) || bot.IsTooManyRequestsError(err) {
		d.Delivery = "pending"
		if saveErr := a.Store.Save(ctx, d); saveErr != nil {
			return saveErr
		}
		return a.notice(ctx, b, d, "Markdown saved; Telegram rejected the channel message. Check permissions or wait if rate limited, then continue publishing.")
	}
	if promptErr := a.prompt(ctx, b, *d); promptErr != nil {
		return errors.Join(err, promptErr)
	}
	return nil
}
