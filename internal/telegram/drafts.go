package telegram

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"html"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"newspaperbot/internal/post"
)

func (a *App) drafts(ctx context.Context, b *bot.Bot, anchor int64, notice string, replace bool) error {
	a.cancelOwnerPreview()
	drafts, err := a.Store.List(ctx)
	if err != nil {
		return err
	}
	active, err := a.Store.Setting(ctx, "active")
	if err != nil {
		return err
	}
	start := 0
	if anchor != 0 {
		start = slices.IndexFunc(drafts, func(d post.Draft) bool { return d.Slot == anchor })
		if start < 0 {
			notice = fmt.Sprintf("Draft <code>#%d</code> unavailable.", anchor)
			start, anchor = 0, 0
		}
	}
	end := min(start+listItemsPerPage, len(drafts))
	var text strings.Builder
	text.WriteString("<b>Drafts</b>")
	markup := emptyKeyboard()
	for _, d := range drafts[start:end] {
		title := d.Title
		if title == "" {
			title = "Untitled"
		}
		fmt.Fprintf(&text, "\n\n<code>#%d</code>", d.Slot)
		if d.ID == active {
			text.WriteString(" · <b>Selected</b>")
		}
		fmt.Fprintf(&text, "\n<b>%s</b>", html.EscapeString(clip(title, 85)))
		markup.InlineKeyboard = append(markup.InlineKeyboard, []models.InlineKeyboardButton{
			{Text: fmt.Sprintf("Preview #%d", d.Slot), CallbackData: fmt.Sprintf("draft-preview:%d", d.ID)},
			{Text: fmt.Sprintf("Edit #%d", d.Slot), CallbackData: fmt.Sprintf("draft-open:%d", d.ID)},
		})
	}
	if len(drafts) == 0 {
		text.WriteString("\nNone yet · /newpost")
	} else if notice == "" {
		notice = "Reply with a draft number to jump."
	}
	if notice != "" {
		text.WriteString("\n\n<i>" + notice + "</i>")
	}
	var navigation []models.InlineKeyboardButton
	if start > 0 {
		navigation = append(navigation, models.InlineKeyboardButton{Text: "‹", CallbackData: fmt.Sprintf("drafts:%d", drafts[max(start-listItemsPerPage, 0)].Slot)})
	}
	navigation = append(navigation, models.InlineKeyboardButton{Text: "Refresh", CallbackData: fmt.Sprintf("drafts:%d", anchor)})
	if end < len(drafts) {
		navigation = append(navigation, models.InlineKeyboardButton{Text: "›", CallbackData: fmt.Sprintf("drafts:%d", drafts[end].Slot)})
	}
	markup.InlineKeyboard = append(markup.InlineKeyboard, navigation)
	if err := a.writeList(ctx, b, "drafts", text.String(), markup, replace); err != nil {
		return err
	}
	return a.Store.SetSetting(ctx, "drafts_anchor", anchor)
}

func (a *App) previewDraft(ctx context.Context, b *bot.Bot, id int64) error {
	d, err := a.Store.Get(ctx, id)
	if errors.Is(err, sql.ErrNoRows) || err == nil && d.Slot == 0 {
		return a.drafts(ctx, b, 0, "Draft unavailable · choose another.", false)
	}
	if err != nil {
		return err
	}
	message, err := a.Store.Setting(ctx, "drafts_message")
	if err != nil {
		return err
	}
	anchor, err := a.Store.Setting(ctx, "drafts_anchor")
	if err != nil {
		return err
	}
	markup := &models.InlineKeyboardMarkup{InlineKeyboard: [][]models.InlineKeyboardButton{{
		{Text: fmt.Sprintf("Edit #%d", d.Slot), CallbackData: fmt.Sprintf("draft-open:%d", d.ID)},
		{Text: "Back", CallbackData: fmt.Sprintf("drafts:%d", anchor)},
	}}}
	text := overview(d)
	if d.Validate() == nil && utf8.RuneCountInString(previewMarkdown(d)) <= 32768 {
		_, err = a.writeText(ctx, b, int(message), "", markup, a.richMessage(d, true))
		if err == nil || unchangedMessage(err) {
			return nil
		}
		if !errors.Is(err, bot.ErrorBadRequest) || missingCard(err) {
			return err
		}
		text += "\n\n<i>Rich preview unavailable · Edit → Download gets the Markdown.</i>"
	}
	_, err = a.writeText(ctx, b, int(message), text, markup, nil)
	if unchangedMessage(err) {
		return nil
	}
	return err
}

// Explicitly opening a draft brings its editor into view. Later source edits
// keep updating this new card in place; the original source bindings survive.
func (a *App) openDraft(ctx context.Context, b *bot.Bot, id int64) error {
	d, err := a.Store.Get(ctx, id)
	if errors.Is(err, sql.ErrNoRows) || err == nil && d.Slot == 0 {
		return a.drafts(ctx, b, 0, "Draft unavailable · choose another.", false)
	}
	if err != nil {
		return err
	}
	if err := a.Store.SetSetting(ctx, "active", d.ID); err != nil {
		return err
	}
	d.ResetView()
	if err := a.Store.Save(ctx, &d); err != nil {
		return err
	}
	if err := a.clearSourceReply(ctx, b, &d); err != nil {
		return err
	}
	old := d.CardID
	d.CardID = 0
	if err := a.render(ctx, b, &d); err != nil {
		return err
	}
	if old != 0 {
		a.deleteCard(ctx, b, old)
	}
	list, err := a.Store.Setting(ctx, "drafts_message")
	if err != nil {
		return err
	}
	if list != 0 {
		if err := a.Store.SetSetting(ctx, "drafts_message", 0); err != nil {
			return err
		}
		a.deleteCard(ctx, b, int(list))
	}
	return nil
}

func (a *App) draftListCallback(ctx context.Context, b *bot.Bot, q *models.CallbackQuery) (bool, error) {
	kind, raw, _ := strings.Cut(q.Data, ":")
	if kind != "drafts" && kind != "draft-preview" && kind != "draft-open" {
		return false, nil
	}
	_, err := b.AnswerCallbackQuery(ctx, &bot.AnswerCallbackQueryParams{CallbackQueryID: q.ID})
	if err != nil {
		return true, err
	}
	message, err := a.Store.Setting(ctx, "drafts_message")
	if err != nil {
		return true, err
	}
	if q.Message.Message == nil || int64(q.Message.Message.ID) != message {
		return true, nil
	}
	number, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || number < 0 || kind != "drafts" && number == 0 {
		return true, nil
	}
	switch kind {
	case "draft-preview":
		return true, a.previewDraft(ctx, b, number)
	case "draft-open":
		return true, a.openDraft(ctx, b, number)
	default:
		return true, a.drafts(ctx, b, number, "", false)
	}
}
