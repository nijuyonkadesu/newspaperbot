package telegram

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"html"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"newspaperbot/internal/post"
)

const articlesPerPage = 8

func articleAge(d post.Draft) string {
	if d.PublishedAt.UTC().Format("2006-01-02") == time.Now().UTC().Format("2006-01-02") {
		return "Today"
	}
	return "Earlier"
}

func articleIdentity(d post.Draft) string {
	return fmt.Sprintf("<code>#%d</code> · %s · %s", d.Number, articleAge(d), d.PublishedAt.UTC().Format("2006-01-02"))
}

func (a *App) liveArticles(ctx context.Context) ([]post.Article, error) {
	if err := a.Repository.Refresh(ctx); err != nil {
		return nil, err
	}
	return a.Repository.Articles(ctx)
}

func (a *App) downloadArticle(ctx context.Context, b *bot.Bot, number int64) error {
	if a.Repository == nil {
		return a.replyHTML(ctx, b, "<b>Live articles</b>\nRepository publishing is not configured.")
	}
	articles, err := a.liveArticles(ctx)
	if err != nil {
		return err
	}
	for _, article := range articles {
		if article.Number == number {
			_, err := sendMarkdown(ctx, b, a.OwnerID, filepath.Base(article.Filename), []byte(article.Original))
			return err
		}
	}
	return a.replyHTML(ctx, b, fmt.Sprintf("<b>Article unavailable</b> · <code>#%d</code>\nUse /posts to browse live articles.", number))
}

func (a *App) articles(ctx context.Context, b *bot.Bot, anchor int64, notice string, replace bool) error {
	a.cancelOwnerPreview()
	if a.Repository == nil {
		return a.replyHTML(ctx, b, "<b>Live articles</b>\nRepository publishing is not configured.")
	}
	articles, err := a.liveArticles(ctx)
	if err != nil {
		return err
	}
	revisions, err := a.Store.Revisions(ctx)
	if err != nil {
		return err
	}
	editing := map[int64]bool{}
	for _, revision := range revisions {
		editing[revision.Number] = true
	}
	start := 0
	if anchor != 0 {
		start = slices.IndexFunc(articles, func(article post.Article) bool { return article.Number == anchor })
		if start < 0 {
			notice = fmt.Sprintf("Article <code>#%d</code> unavailable · reply with another number.", anchor)
			anchor, err = a.Store.Setting(ctx, "posts_anchor")
			if err != nil {
				return err
			}
			start = slices.IndexFunc(articles, func(article post.Article) bool { return article.Number == anchor })
			if start < 0 {
				start, anchor = 0, 0
			}
		}
	}
	end := min(start+articlesPerPage, len(articles))
	var text strings.Builder
	text.WriteString("<b>Live articles</b>")
	if start < end {
		fmt.Fprintf(&text, " · <code>#%d–#%d</code>", articles[start].Number, articles[end-1].Number)
	}
	markup := emptyKeyboard()
	section := ""
	for i := start; i < end; i++ {
		d := articles[i].Draft
		if age := articleAge(d); age != section {
			section = age
			fmt.Fprintf(&text, "\n\n<b>%s</b>", section)
		}
		fmt.Fprintf(&text, "\n\n<code>#%d</code> · %s", d.Number, d.PublishedAt.Format("2006-01-02"))
		if editing[d.Number] {
			text.WriteString(" · <b>Editing</b>")
		}
		fmt.Fprintf(&text, "\n<b>%s</b>", html.EscapeString(clip(d.Title, 85)))
		markup.InlineKeyboard = append(markup.InlineKeyboard, []models.InlineKeyboardButton{
			{Text: fmt.Sprintf("Preview #%d", d.Number), CallbackData: fmt.Sprintf("preview:%d", d.Number)},
			{Text: fmt.Sprintf("Edit #%d", d.Number), CallbackData: fmt.Sprintf("article:%d", d.Number)},
		})
	}
	if len(articles) == 0 {
		text.Reset()
		text.WriteString("<b>Live articles</b>\nNone published yet.")
	}
	if notice == "" && len(articles) > 0 {
		notice = "Reply with an article number to jump."
	}
	if notice != "" {
		text.WriteString("\n\n<i>" + notice + "</i>")
	}
	navigation := []models.InlineKeyboardButton{}
	if start > 0 {
		previous := max(start-articlesPerPage, 0)
		previousAnchor := articles[previous].Number
		if previous == 0 {
			previousAnchor = 0
		}
		navigation = append(navigation, models.InlineKeyboardButton{Text: "‹", CallbackData: fmt.Sprintf("posts:%d", previousAnchor)})
	}
	navigation = append(navigation, models.InlineKeyboardButton{Text: "Refresh", CallbackData: fmt.Sprintf("posts:%d", anchor)})
	if end < len(articles) {
		navigation = append(navigation, models.InlineKeyboardButton{Text: "›", CallbackData: fmt.Sprintf("posts:%d", articles[end].Number)})
	}
	markup.InlineKeyboard = append(markup.InlineKeyboard, navigation)
	id, err := a.Store.Setting(ctx, "posts_message")
	if err != nil {
		return err
	}
	if id != 0 && !replace {
		_, err := b.EditMessageText(ctx, &bot.EditMessageTextParams{ChatID: a.OwnerID, MessageID: int(id), Text: text.String(), ParseMode: models.ParseModeHTML, ReplyMarkup: markup})
		if err == nil || unchangedMessage(err) {
			return a.Store.SetSetting(ctx, "posts_anchor", anchor)
		}
		if !missingCard(err) {
			return err
		}
	}
	message, err := b.SendMessage(ctx, &bot.SendMessageParams{ChatID: a.OwnerID, Text: text.String(), ParseMode: models.ParseModeHTML, ReplyMarkup: markup, DisableNotification: true})
	if err != nil {
		return err
	}
	if err := a.Store.SetSetting(ctx, "posts_message", int64(message.ID)); err != nil {
		a.deleteCard(ctx, b, message.ID)
		return err
	}
	if id != 0 {
		a.deleteCard(ctx, b, int(id))
	}
	return a.Store.SetSetting(ctx, "posts_anchor", anchor)
}

func (a *App) previewArticle(ctx context.Context, b *bot.Bot, number int64) error {
	if a.Repository == nil {
		return a.articles(ctx, b, 0, "", false)
	}
	articles, err := a.liveArticles(ctx)
	if err != nil {
		return err
	}
	i := slices.IndexFunc(articles, func(article post.Article) bool { return article.Number == number })
	if i < 0 {
		return a.articles(ctx, b, number, "", false)
	}
	d := articles[i].Draft
	if saved, err := a.Store.GetArticle(ctx, number); err == nil {
		d.Images = saved.Images
	}
	d.Categories, d.AvailableTags = []string{d.Category}, d.Tags
	id, err := a.Store.Setting(ctx, "posts_message")
	if err != nil {
		return err
	}
	anchor, err := a.Store.Setting(ctx, "posts_anchor")
	if err != nil {
		return err
	}
	markup := &models.InlineKeyboardMarkup{InlineKeyboard: [][]models.InlineKeyboardButton{{
		{Text: fmt.Sprintf("Edit #%d", number), CallbackData: fmt.Sprintf("article:%d", number)},
		{Text: "Back", CallbackData: fmt.Sprintf("posts:%d", anchor)},
	}}}
	markdown := previewMarkdown(d)
	text := "<b>Live</b> · " + articleIdentity(d) + "\n\n" + overview(d)
	if utf8.RuneCountInString(markdown) <= 32768 {
		_, err = a.writeText(ctx, b, int(id), "", markup, a.richMessage(d, true))
		if err == nil || unchangedMessage(err) {
			return nil
		}
		if !errors.Is(err, bot.ErrorBadRequest) || missingCard(err) {
			return err
		}
		a.logError(b, "article preview", err)
		text += "\n\n<i>Rich preview unavailable · Edit → Download gets the Markdown.</i>"
	} else {
		text += "\n\n<i>Excerpt · Edit → Download gets the full Markdown.</i>"
	}
	_, err = a.writeText(ctx, b, int(id), text, markup, nil)
	if unchangedMessage(err) {
		return nil
	}
	return err
}

func (a *App) articleListReply(ctx context.Context, b *bot.Bot, m *models.Message) (bool, error) {
	text := strings.TrimSpace(m.Text)
	if m.ReplyToMessage == nil || strings.HasPrefix(text, "/") {
		return false, nil
	}
	id, err := a.Store.Setting(ctx, "posts_message")
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
		anchor, err = a.Store.Setting(ctx, "posts_anchor")
		if err != nil {
			return true, err
		}
		notice = "Reply with a positive article number, e.g. <code>125</code>."
	}
	if err := a.articles(ctx, b, anchor, notice, false); err != nil {
		return true, err
	}
	a.deleteOwnerMessage(ctx, b, m.ID, "article list reply")
	return true, nil
}

func (a *App) editArticle(ctx context.Context, b *bot.Bot, number int64) error {
	if a.Repository == nil {
		return a.replyHTML(ctx, b, "<b>Live articles</b>\nRepository publishing is not configured.")
	}
	d, err := a.Store.GetArticle(ctx, number)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if d.Revision != nil {
		if err := a.Store.SetSetting(ctx, "active", d.ID); err != nil {
			return err
		}
		return a.render(ctx, b, &d)
	}
	articles, err := a.liveArticles(ctx)
	if err != nil {
		return err
	}
	for _, article := range articles {
		if article.Number != number {
			continue
		}
		d, err = a.Store.StartRevision(ctx, article)
		if err != nil {
			if d.ID != 0 {
				return a.notice(ctx, b, &d, err.Error())
			}
			return err
		}
		a.refreshChoices(ctx, b, &d)
		if err := a.Store.Save(ctx, &d); err != nil {
			return err
		}
		return a.render(ctx, b, &d)
	}
	return a.replyHTML(ctx, b, fmt.Sprintf("<b>Article unavailable</b> · <code>#%d</code>\nUse /posts to browse live articles.", number))
}

func (a *App) discardChanges(ctx context.Context, b *bot.Bot, d *post.Draft, reload bool) error {
	restored, err := a.Store.DiscardRevision(ctx, d.ID)
	if err != nil {
		return a.notice(ctx, b, d, err.Error())
	}
	*d = restored
	if reload {
		return a.editArticle(ctx, b, d.Number)
	}
	return a.render(ctx, b, d)
}

func (a *App) articleCallback(ctx context.Context, b *bot.Bot, q *models.CallbackQuery) (bool, error) {
	kind, raw, _ := strings.Cut(q.Data, ":")
	if kind != "posts" && kind != "article" && kind != "preview" {
		return false, nil
	}
	number, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || number < 0 || kind != "posts" && number == 0 {
		return true, nil
	}
	_, err = b.AnswerCallbackQuery(ctx, &bot.AnswerCallbackQueryParams{CallbackQueryID: q.ID})
	if err != nil {
		return true, err
	}
	id, err := a.Store.Setting(ctx, "posts_message")
	if err != nil {
		return true, err
	}
	if q.Message.Message == nil || int64(q.Message.Message.ID) != id {
		return true, nil
	}
	if kind == "preview" {
		return true, a.previewArticle(ctx, b, number)
	}
	if kind == "article" {
		return true, a.editArticle(ctx, b, number)
	}
	return true, a.articles(ctx, b, number, "", false)
}
