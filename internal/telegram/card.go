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

const example = "My title\n\nA short summary.\n\n## First section\nWrite the Markdown body here."
const choicesPerPage = 8

func clip(text string, limit int) string {
	runes := []rune(text)
	if len(runes) > limit {
		return string(runes[:limit]) + "…"
	}
	return text
}

// Each call builds one horizontal row. Panels add rows deliberately.
func keyboard(d post.Draft, labelsAndActions ...string) *models.InlineKeyboardMarkup {
	row := []models.InlineKeyboardButton{}
	for i := 0; i < len(labelsAndActions); i += 2 {
		row = append(row, models.InlineKeyboardButton{Text: labelsAndActions[i], CallbackData: fmt.Sprintf("draft:%d:%d:%s", d.ID, d.UpdatedAt.UnixNano(), labelsAndActions[i+1])})
	}
	return &models.InlineKeyboardMarkup{InlineKeyboard: [][]models.InlineKeyboardButton{row}}
}

func emptyKeyboard() *models.InlineKeyboardMarkup {
	return &models.InlineKeyboardMarkup{InlineKeyboard: [][]models.InlineKeyboardButton{}}
}

func addRow(markup *models.InlineKeyboardMarkup, d post.Draft, items ...string) {
	markup.InlineKeyboard = append(markup.InlineKeyboard, keyboard(d, items...).InlineKeyboard[0])
}

func draftKeyboard(d post.Draft, preview bool) *models.InlineKeyboardMarkup {
	label, action := "Preview", "preview"
	if preview {
		label, action = "Back", "back"
	}
	saveLabel, saveAction := "Publish", "publish"
	if d.Revision != nil {
		saveLabel, saveAction = "Save changes", "save"
	}
	markup := keyboard(d, saveLabel, saveAction, label, action, "Options", "options")
	addRow(markup, d, "Category", "categories-0", "Tags", "tags-0")
	if d.Revision != nil {
		addRow(markup, d, "Discard changes", "cancel")
	}
	return markup
}

func previewMarkdown(d post.Draft) string {
	if d.Number != 0 && d.Revision == nil {
		d.Categories, d.AvailableTags = []string{d.Category}, d.Tags
	}
	tags := strings.Join(d.TagLabels(), ", ")
	if tags == "" {
		tags = "none"
	}
	escape := func(text string) string { return bot.EscapeMarkdown(html.EscapeString(text)) }
	identity := fmt.Sprintf("`#%d`", d.Slot)
	if d.Number != 0 {
		label := "Live"
		if d.Revision != nil {
			label = "Editing live"
		}
		identity = fmt.Sprintf("**%s** · `#%d` · %s · %s", label, d.Number, articleAge(d), d.PublishedAt.Format("2006-01-02"))
	}
	return d.RichMarkdown() + fmt.Sprintf("\n\n---\n\n%s\n\n**Category:** %s\n\n**Tags:** %s", identity, escape(d.CategoryLabel()), escape(tags)) + messageIssuesMarkdown(d.MessageIssues) + statusMarkdown(d.Notice)
}

func statusMarkdown(status string) string {
	if status == "" {
		return ""
	}
	return "\n\n**Status** · " + bot.EscapeMarkdown(status)
}

func messageIssuesMarkdown(issues []post.MessageIssue) string {
	if len(issues) == 0 {
		return ""
	}
	var text strings.Builder
	text.WriteString("\n\n**Review**\n")
	for i, issue := range issues {
		fmt.Fprintf(&text, "\n%d. %s · `/remove %d`", i+1, bot.EscapeMarkdown(issue.Reason), issue.MessageID)
	}
	return text.String()
}

func messageIssuesHTML(issues []post.MessageIssue) string {
	if len(issues) == 0 {
		return ""
	}
	var text strings.Builder
	text.WriteString("\n\n<b>Review</b>")
	for i, issue := range issues {
		fmt.Fprintf(&text, "\n%d. %s · <code>/remove %d</code>", i+1, html.EscapeString(issue.Reason), issue.MessageID)
	}
	return text.String()
}

func overview(d post.Draft) string {
	tags := strings.Join(d.TagLabels(), ", ")
	if tags == "" {
		tags = "no tags"
	}
	text := fmt.Sprintf("<b>%s</b>\n%s\n\n<blockquote>%s</blockquote>",
		html.EscapeString(clip(d.Title, 200)), html.EscapeString(clip(d.Summary, 400)), html.EscapeString(clip(d.Content, 650)))
	if d.Slot > 0 {
		text += fmt.Sprintf("\n\n<code>#%d</code> <i>· %s · %s</i>", d.Slot, html.EscapeString(clip(d.CategoryLabel(), 40)), html.EscapeString(clip(tags, 80)))
	} else {
		text += fmt.Sprintf("\n\n<i>%s · %s</i>", html.EscapeString(clip(d.CategoryLabel(), 40)), html.EscapeString(clip(tags, 80)))
	}
	if d.Revision != nil {
		text = "<b>Editing live</b> · " + articleIdentity(d) + "\n\n" + text
	}
	return text
}

func card(d post.Draft) (string, *models.InlineKeyboardMarkup) {
	if d.Number != 0 && d.Revision == nil {
		d.Categories, d.AvailableTags = []string{d.Category}, d.Tags
	}
	text := overview(d)
	markup := draftKeyboard(d, false)
	switch {
	case d.Revision != nil && d.Locked():
		status := "Saving changes…"
		markup = keyboard(d, "Download changes", "download")
		if d.Revision.Conflict {
			status = "Article changed on main"
			addRow(markup, d, "Discard & reload", "reload", "Discard changes", "cancel")
		} else if d.Revision.Applied {
			status = "Repository updated · channel update pending"
			addRow(markup, d, "Retry channel update", "save")
		} else if d.GitState == "failed" {
			status = "Changes saved locally · saving paused"
			addRow(markup, d, "Retry saving", "save")
		}
		if d.Notice != "" {
			status = d.Notice
		}
		text += "\n\n<b>Status</b> · " + html.EscapeString(status)
	case d.GitOperation != "" && d.Number == 0:
		text += "\n\nPublishing…"
		markup = keyboard(d, "Download", "download")
		if d.GitState == "failed" {
			text = overview(d) + "\n\nPublication paused. Your post is saved."
			markup = keyboard(d, "Retry publish", "publish", "Download", "download")
		}
	case d.Number != 0 && d.Revision == nil:
		status := "Export pending"
		if d.Exported {
			status = "Saved · " + html.EscapeString(d.Filename)
			if d.CommitSHA != "" {
				status = "Committed to main · <code>" + html.EscapeString(d.Filename) + "</code>"
			}
		}
		text = fmt.Sprintf("<b>Live</b> · %s\n\n<b>%s</b>\n%s\n\n%s", articleIdentity(d), html.EscapeString(d.Title), html.EscapeString(clip(d.Summary, 400)), status)
		label, action := "Preview", "preview"
		if d.View == "preview" {
			label, action = "Back", "back"
			text = "<b>Live</b> · " + articleIdentity(d) + "\n\n" + overview(d)
		}
		markup = keyboard(d, label, action, "Download", "download")
		if d.Portfolio && d.Exported && (d.ChannelID == 0 || d.Delivery == "sent") {
			markup = keyboard(d, "Edit", "edit", label, action, "Download", "download")
		}
		if !d.Exported || d.ChannelID != 0 && d.Delivery != "sent" {
			markup = keyboard(d, "Continue publishing", "publish", "Download", "download")
		}
		if d.ChannelID != 0 {
			text += "\nDestination · " + html.EscapeString(d.Delivery)
		}
		if d.Delivery == "uncertain" {
			text += "\n\nCheck the destination first. Retrying may duplicate the last message."
			markup = keyboard(d, "Checked destination · retry", "retry", "Download", "download")
		}
	case d.View == "replace" || d.Step == post.Compose:
		source := example
		if d.View == "replace" && d.Title != "" {
			source = d.Title + "\n\n" + d.Summary + "\n\n" + d.Content
		}
		tags := strings.Join(d.TagLabels(), ", ")
		if tags == "" {
			tags = "none"
		}
		text = fmt.Sprintf("<code>#%d</code>\nSend title, summary, and Markdown body in <b>one message</b>:\n\n<pre>%s</pre>\n\n<i>Edit your message to correct it. Further messages append to the body. Category: %s · tags: %s.</i>",
			d.Slot, html.EscapeString(clip(source, 1100)), html.EscapeString(clip(d.CategoryLabel(), 40)), html.EscapeString(clip(tags, 80)))
		markup = keyboard(d, "Cancel draft", "cancel")
		if d.View == "replace" {
			text += "\nYour current post stays saved until a valid replacement arrives."
			markup = keyboard(d, "Keep current post", "back", "Cancel draft", "cancel")
		}
		if d.Revision != nil {
			text = "<b>Editing live</b> · " + articleIdentity(d) + "\n\nSend replacement title, summary, and Markdown body in <b>one message</b>.\n\n<pre>" + html.EscapeString(clip(source, 1100)) + "</pre>"
			markup = keyboard(d, "Back", "back", "Discard changes", "cancel")
		}
		text += "\n<i>Reply to this card to target this post.</i>"
	case d.View == "options":
		text += "\n\n<b>Options</b>"
		markup = keyboard(d, "Category", "categories-0", "Tags", "tags-0")
		addRow(markup, d, "Replace post", "replace", "Download", "download")
		if len(d.Sources) > 0 && !d.Sources[len(d.Sources)-1].Full {
			addRow(markup, d, "Remove last text addition", "undo")
		}
		addRow(markup, d, "Back", "back", "Cancel draft", "cancel")
		if d.Revision != nil {
			markup.InlineKeyboard[len(markup.InlineKeyboard)-1][1].Text = "Discard changes"
		}
		if d.PendingContent != "" {
			text += "\nAn unfinished body replacement from the old flow is also saved."
			addRow(markup, d, "Use unfinished replacement", "recover-pending")
		}
	case strings.HasPrefix(d.View, "categories-") || strings.HasPrefix(d.View, "tags-"):
		kind, rawPage, _ := strings.Cut(d.View, "-")
		names := d.Categories
		if kind == "tags" {
			names = d.AvailableTags
		}
		page, _ := strconv.Atoi(rawPage)
		page = min(max(page, 0), max((len(names)-1)/choicesPerPage, 0))
		label := "Category"
		if kind == "tags" {
			label = "Tags"
		}
		text += fmt.Sprintf("\n\n<b>%s · %d/%d</b>", label, page+1, max((len(names)+choicesPerPage-1)/choicesPerPage, 1))
		markup = emptyKeyboard()
		for i := page * choicesPerPage; i < min((page+1)*choicesPerPage, len(names)); i += 2 {
			items := []string{}
			for j := i; j < min(i+2, len(names), (page+1)*choicesPerPage); j++ {
				label := clip(names[j], 25)
				if kind == "tags" && slices.Contains(d.Tags, names[j]) || kind == "categories" && d.Category == names[j] {
					label = "✓ " + label
				}
				action := "category"
				if kind == "tags" {
					action = "tag"
				}
				items = append(items, label, fmt.Sprintf("%s-%d", action, j))
			}
			addRow(markup, d, items...)
		}
		if kind == "tags" {
			addRow(markup, d, "No tags", "clear-tags")
		}
		navigation := []string{}
		if page > 0 {
			navigation = append(navigation, "‹", fmt.Sprintf("%s-%d", kind, page-1))
		}
		if (page+1)*choicesPerPage < len(names) {
			navigation = append(navigation, "›", fmt.Sprintf("%s-%d", kind, page+1))
		}
		navigation = append(navigation, "Back", "back")
		addRow(markup, d, navigation...)
	case d.View == "preview":
		markup = draftKeyboard(d, true)
	default:
		if d.Revision != nil {
			text += "\n<i>Reply to append · /replace rewrites</i>"
		} else {
			text += "\n<i>Saved · edit your message to revise; send more to append.</i>"
		}
	}
	if d.Invalid != "" {
		text += "\n\n<b>" + html.EscapeString(clip(d.Invalid, 350)) + "</b>"
		if d.View == "" || d.View == "preview" {
			markup = keyboard(d, "Replace post", "replace", "Options", "options")
			addRow(markup, d, "Category", "categories-0", "Tags", "tags-0")
		}
	}
	if !d.Locked() && (d.CategoryLabel() != d.Category || strings.Join(d.TagLabels(), ",") != strings.Join(d.Tags, ",")) {
		text += "\n<i>* new value · added when published</i>"
	}
	if d.Notice != "" && !(d.Revision != nil && d.Locked()) {
		text += "\n\n<b>Status</b> · " + html.EscapeString(clip(d.Notice, 350))
	}
	text += messageIssuesHTML(d.MessageIssues)
	sourceButtons(d, markup)
	return text, markup
}

func (a *App) prompt(ctx context.Context, b *bot.Bot, d post.Draft) error {
	return a.render(ctx, b, &d)
}

// RestoreCard upgrades the selected card and other recent unfinished cards.
// Existing views survive restart; drafts without a card are not announced.
func (a *App) RestoreCard(ctx context.Context, b *bot.Bot) error {
	active, err := a.Store.Active(ctx)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if active.ID != 0 {
		if a.Repository != nil && !active.Locked() {
			a.refreshChoices(ctx, b, &active)
			active.Portfolio = true
			if err := a.Store.Save(ctx, &active); err != nil {
				return err
			}
		}
		if err := a.render(ctx, b, &active); err != nil {
			return err
		}
	}
	drafts, err := a.Store.Cards(ctx)
	if err != nil {
		return err
	}
	var failures []error
	for _, d := range drafts {
		if d.ID == active.ID || d.CardID == 0 || d.Number != 0 && d.Revision == nil {
			continue
		}
		if a.Repository != nil && !d.Locked() {
			a.refreshChoices(ctx, b, &d)
			d.Portfolio = true
			if err := a.Store.Save(ctx, &d); err != nil {
				failures = append(failures, err)
				continue
			}
		}
		if err := a.render(ctx, b, &d); err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

// render edits the tracked card in place, creating one only when it is missing.
func (a *App) render(ctx context.Context, b *bot.Bot, d *post.Draft) error {
	if err := a.clearSourceReply(ctx, b, d); err != nil {
		a.logError(b, "source reply cleanup", err)
	}
	if d.View == "paused" {
		d.View = "" // Older builds hid controls on drafts other than the selected one.
		if err := a.Store.Save(ctx, d); err != nil {
			return err
		}
	}
	tooLong := d.View == "preview" && utf8.RuneCountInString(previewMarkdown(*d)) > 32768
	if tooLong {
		d.Notice = "This post is too long for an inline preview. /download contains the complete Markdown."
		if err := a.Store.Save(ctx, d); err != nil {
			return err
		}
	}
	text, markup := card(*d)
	var rich *models.InputRichMessage
	live := d.Number != 0 && d.Revision == nil && d.Exported && (d.ChannelID == 0 || d.Delivery == "sent")
	if d.View == "preview" && d.Invalid == "" && !tooLong && (!d.Locked() || live) {
		rich = &models.InputRichMessage{Markdown: previewMarkdown(*d)}
	}
	err := a.writeCard(ctx, b, d, text, markup, rich)
	if rich != nil && errors.Is(err, bot.ErrorBadRequest) {
		d.Notice = "Telegram could not render this Markdown. Your source is saved; /download gets the complete file."
		if saveErr := a.Store.Save(ctx, d); saveErr != nil {
			return saveErr
		}
		text, markup = card(*d)
		return a.writeCard(ctx, b, d, text, markup, nil)
	}
	return err
}

func missingCard(err error) bool {
	if !errors.Is(err, bot.ErrorBadRequest) {
		return false
	}
	text := strings.ToLower(err.Error())
	return strings.Contains(text, "message to edit not found") || strings.Contains(text, "message can't be edited")
}

func (a *App) writeCard(ctx context.Context, b *bot.Bot, d *post.Draft, text string, markup *models.InlineKeyboardMarkup, rich *models.InputRichMessage) error {
	write := func(id int) (*models.Message, error) {
		return a.writeText(ctx, b, id, text, markup, rich)
	}
	if d.CardID != 0 {
		_, err := write(d.CardID)
		if err == nil || unchangedMessage(err) {
			return nil
		}
		if !missingCard(err) {
			return err
		}
	}
	message, err := write(0)
	if err != nil {
		return err
	}
	oldID := d.CardID
	d.CardID = message.ID
	if err := a.Store.SaveCard(ctx, d); err != nil {
		a.deleteCard(ctx, b, message.ID)
		d.CardID = oldID
		return err
	}
	if oldID != 0 {
		a.deleteCard(ctx, b, oldID)
	}
	return nil
}

func (a *App) deleteCard(ctx context.Context, b *bot.Bot, id int) bool {
	if _, err := b.DeleteMessage(ctx, &bot.DeleteMessageParams{ChatID: a.OwnerID, MessageID: id}); err != nil {
		// Old cards may exceed the deletion window. Remove their controls instead.
		a.logError(b, "card cleanup", err)
		if _, editErr := b.EditMessageReplyMarkup(ctx, &bot.EditMessageReplyMarkupParams{ChatID: a.OwnerID, MessageID: id, ReplyMarkup: emptyKeyboard()}); editErr != nil {
			a.logError(b, "old card controls", editErr)
		}
		return false
	}
	return true
}
