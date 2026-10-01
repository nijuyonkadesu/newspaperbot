package telegram

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"html"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"newspaperbot/internal/metadata"
	"newspaperbot/internal/post"
)

func taxonomyText(c metadata.Catalog) (string, error) {
	var text strings.Builder
	text.WriteString("<b>Categories</b> · choose one\n")
	for _, name := range c.Categories {
		fmt.Fprintf(&text, "<code>%s</code>\n", html.EscapeString(name))
	}
	text.WriteString("\n<b>Tags</b> · choose any\n")
	for _, name := range c.Tags {
		fmt.Fprintf(&text, "<code>%s</code>\n", html.EscapeString(name))
	}
	if len(c.Tags) == 0 {
		text.WriteString("No tags available.\n")
	}
	tags := "-"
	if len(c.Tags) > 0 {
		tags = strings.Join(c.Tags[:min(2, len(c.Tags))], ", ")
	}
	fmt.Fprintf(&text, "\n<b>Optional post footer</b> · last two lines\n<pre>Category: %s\nTags: %s</pre>\n<i>Pin this list. It refreshes in place when the sources change. Use - for no tags.</i>", html.EscapeString(c.Categories[0]), html.EscapeString(tags))
	result := text.String()
	if utf8.RuneCountInString(html.UnescapeString(result)) > 4096 {
		return "", errors.New("category/tag list is too long for a single Telegram message")
	}
	return result, nil
}

// SyncTaxonomy edits the last /taxonomy output, preserving its message ID and pin.
// Without an explicit command it never creates a new message.
func (a *App) SyncTaxonomy(ctx context.Context, b *bot.Bot, requested bool) error {
	a.taxonomyMu.Lock()
	defer a.taxonomyMu.Unlock()
	id, err := a.Store.Setting(ctx, "taxonomy_message")
	if err != nil || id == 0 && !requested {
		return err
	}
	c, err := a.catalog(ctx)
	if err != nil {
		return err
	}
	text, err := taxonomyText(c)
	if err != nil {
		return err
	}
	sum := sha256.Sum256([]byte(text))
	hash := int64(binary.BigEndian.Uint64(sum[:8]))
	previous, err := a.Store.Setting(ctx, "taxonomy_hash")
	if err != nil {
		return err
	}
	if id != 0 && hash == previous && !requested {
		return nil
	}
	if id != 0 {
		_, err = b.EditMessageText(ctx, &bot.EditMessageTextParams{ChatID: a.OwnerID, MessageID: int(id), Text: text, ParseMode: models.ParseModeHTML})
		if err != nil && !(errors.Is(err, bot.ErrorBadRequest) && strings.Contains(strings.ToLower(err.Error()), "message is not modified")) {
			if !missingCard(err) {
				return err
			}
			if !requested {
				return a.Store.SetSetting(ctx, "taxonomy_message", 0)
			}
			id = 0
		}
	}
	if id == 0 {
		message, err := b.SendMessage(ctx, &bot.SendMessageParams{ChatID: a.OwnerID, Text: text, ParseMode: models.ParseModeHTML, DisableNotification: true})
		if err != nil {
			return err
		}
		if err := a.Store.SetSetting(ctx, "taxonomy_message", int64(message.ID)); err != nil {
			a.deleteCard(ctx, b, message.ID)
			return err
		}
	}
	return a.Store.SetSetting(ctx, "taxonomy_hash", hash)
}

func (a *App) RunTaxonomySync(ctx context.Context, b *bot.Bot) {
	sync := func() {
		checkCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		if a.Repository != nil {
			if err := a.Repository.Refresh(checkCtx); err != nil && ctx.Err() == nil {
				a.logError(b, "repository refresh", err)
			}
		}
		if err := a.SyncTaxonomy(checkCtx, b, false); err != nil && ctx.Err() == nil {
			a.logError(b, "taxonomy refresh", err)
		}
	}
	sync()
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			sync()
		}
	}
}

// On a temporary source failure, keep authoring with the last saved catalog.
func (a *App) refreshChoices(ctx context.Context, b *bot.Bot, d *post.Draft) {
	c, err := a.catalog(ctx)
	if err != nil {
		a.logError(b, "draft taxonomy", err)
		return
	}
	d.Categories, d.AvailableTags = c.Categories, c.Tags
	d.TagGroups = c.Groups
	d.OrderTags()
}
