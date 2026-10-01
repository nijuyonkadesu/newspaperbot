package telegram

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"tgblogbot/internal/metadata"
	"tgblogbot/internal/post"
	"tgblogbot/internal/store"
)

type App struct {
	taxonomyMu sync.Mutex
	OwnerID    int64
	Store      *store.Store
	Metadata   metadata.Loader
	OutputDir  string
	WriteFile  func(string, []byte) error
}

const help = `Write a post in one message:

Title

Short summary.

Markdown body.

/newpost — new draft (you can include the post after the command)
Edit your original message to update the draft. More messages append to the body.
The bot keeps one current card. Publish saves Markdown and posts to your channel.

/taxonomy — copyable categories and tags (pin the list)
Optional final two lines: Category: name and Tags: tag1, tag2 (or -).

/drafts · /resume <id> — saved drafts
/replace — replace the whole post in one message
/undo — remove the last body addition
/preview — rendered preview on the same card
/download — download the Markdown file
/publish — publish the active draft
/cancel — delete the active unfinished draft
/delete <id> — delete a saved draft (/delete uses the active draft)
/setchannel <@name or ID> · /unsetchannel
/help — show this help

Category and Tags are directly on each draft card. No Done step.
You can keep several drafts open: edit their source messages or use their cards.
Reply to a draft's card/source to add text there. Unthreaded text goes to the last
draft selected with /newpost, /resume, or Replace post. Replying also targets draft commands.
Deleting a source message in Telegram does not remove saved content; use /undo.`

func (a *App) Handle(ctx context.Context, b *bot.Bot, update *models.Update) {
	var err error
	switch {
	case update.CallbackQuery != nil:
		q := update.CallbackQuery
		if q.From.ID != a.OwnerID || !a.privateChat(q.Message.Message) {
			return
		}
		err = a.callback(ctx, b, q)
	case a.allowed(update.EditedMessage):
		err = a.edited(ctx, b, update.EditedMessage, update.ID)
	case a.allowed(update.Message):
		err = a.message(ctx, b, update.Message, update.ID)
	default:
		return
	}
	if err != nil {
		a.logError(b, "update", err)
		d, loadErr := a.errorDraft(ctx, update)
		if loadErr == nil {
			if notifyErr := a.notice(ctx, b, &d, "Could not finish this action. Saved content is retained; try again."); notifyErr != nil {
				a.logError(b, "draft card", notifyErr)
			}
		} else if replyErr := a.reply(ctx, b, "Could not finish this action. Try again; saved drafts are retained.", nil); replyErr != nil {
			a.logError(b, "error reply", replyErr)
		}
	}
}

func (a *App) errorDraft(ctx context.Context, update *models.Update) (post.Draft, error) {
	if q := update.CallbackQuery; q != nil {
		parts := strings.Split(q.Data, ":")
		if len(parts) != 4 {
			return post.Draft{}, sql.ErrNoRows
		}
		id, err := strconv.ParseInt(parts[1], 10, 64)
		if err != nil {
			return post.Draft{}, err
		}
		return a.Store.Get(ctx, id)
	}
	if m := update.EditedMessage; m != nil {
		return a.Store.FromMessage(ctx, m.ID)
	}
	if m := update.Message; m != nil {
		if m.ReplyToMessage != nil {
			return a.Store.FromMessage(ctx, m.ReplyToMessage.ID)
		}
		fields := strings.Fields(m.Text)
		if len(fields) > 0 {
			switch strings.SplitN(fields[0], "@", 2)[0] {
			case "/start", "/help", "/taxonomy", "/drafts", "/setchannel", "/unsetchannel", "/delete", "/cancel":
				return post.Draft{}, sql.ErrNoRows
			case "/resume":
				if len(fields) == 2 {
					id, err := strconv.ParseInt(fields[1], 10, 64)
					if err == nil {
						return a.Store.Get(ctx, id)
					}
				}
				return post.Draft{}, sql.ErrNoRows
			}
		}
	}
	return a.Store.Active(ctx)
}

func (a *App) logError(b *bot.Bot, label string, err error) {
	log.Printf("%s: %s", label, strings.ReplaceAll(err.Error(), b.Token(), "[redacted]"))
}

func (a *App) allowed(m *models.Message) bool {
	return a.privateChat(m) && m.From != nil && m.From.ID == a.OwnerID
}

func (a *App) privateChat(m *models.Message) bool {
	return m != nil && m.Chat.Type == models.ChatTypePrivate && m.Chat.ID == a.OwnerID
}

func (a *App) reply(ctx context.Context, b *bot.Bot, text string, markup models.ReplyMarkup) error {
	_, err := b.SendMessage(ctx, &bot.SendMessageParams{ChatID: a.OwnerID, Text: text, ReplyMarkup: markup, DisableNotification: true})
	return err
}

func (a *App) message(ctx context.Context, b *bot.Bot, m *models.Message, updateID int64) error {
	fields := strings.Fields(m.Text)
	if len(fields) > 0 && strings.HasPrefix(fields[0], "/") {
		command := strings.SplitN(fields[0], "@", 2)[0]
		switch command {
		case "/start", "/help":
			if command == "/start" {
				if err := a.RegisterMenu(ctx, b); err != nil {
					a.logError(b, "command menu", err)
				}
			}
			return a.reply(ctx, b, help, nil)
		case "/taxonomy":
			if err := a.SyncTaxonomy(ctx, b, true); err != nil {
				a.logError(b, "taxonomy command", err)
				return a.reply(ctx, b, "Could not refresh categories and tags. The last list is retained; try /taxonomy again.", nil)
			}
			return nil
		case "/newpost":
			d, err := a.Store.Active(ctx)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return err
			}
			if d.ID != 0 && m.ID <= d.LastMessageID {
				return nil
			}
			if err != nil || !d.Empty() {
				catalog, err := a.Metadata.Taxonomy(ctx)
				if err != nil {
					return fmt.Errorf("new draft metadata: %w", err)
				}
				d, err = a.Store.New(ctx, catalog.Categories, catalog.Tags)
				if err != nil {
					return err
				}
			}
			d.View, d.Notice = "", ""
			if len(fields) > 1 {
				a.refreshChoices(ctx, b, &d)
				if err := d.Replace(post.Source{MessageID: m.ID, UpdateID: updateID, Text: m.Text}); err != nil {
					d.Notice = err.Error()
					d.Sources = []post.Source{{MessageID: m.ID, UpdateID: updateID, Text: m.Text, Full: true}}
				}
			}
			d.LastMessageID = m.ID
			if err := a.Store.Save(ctx, &d); err != nil {
				return err
			}
			return a.render(ctx, b, &d)
		case "/cancel", "/delete":
			if len(fields) > 2 || command == "/cancel" && len(fields) != 1 {
				return a.reply(ctx, b, "Use /cancel for the active draft or /delete <draft ID>.", nil)
			}
			id, err := a.Store.Setting(ctx, "active")
			if err != nil {
				return err
			}
			if len(fields) == 2 {
				id, err = strconv.ParseInt(fields[1], 10, 64)
				if err != nil || id <= 0 {
					return a.reply(ctx, b, "Use a positive draft ID from /drafts.", nil)
				}
			} else if m.ReplyToMessage != nil {
				d, err := a.target(ctx, b, m)
				if err != nil || d.ID == 0 {
					return err
				}
				id = d.ID
			}
			if id == 0 {
				if command == "/delete" {
					return a.reply(ctx, b, "No active draft. Use /delete <id> from /drafts.", nil)
				}
				return nil
			}
			return a.discard(ctx, b, id, m.ID)
		case "/drafts":
			return a.list(ctx, b)
		case "/resume":
			if len(fields) != 2 {
				return a.reply(ctx, b, "Use /resume <draft ID> from /drafts.", nil)
			}
			id, err := strconv.ParseInt(fields[1], 10, 64)
			if err != nil || id <= 0 {
				return a.reply(ctx, b, "Use a positive draft ID from /drafts.", nil)
			}
			d, err := a.Store.Get(ctx, id)
			if errors.Is(err, sql.ErrNoRows) {
				return a.reply(ctx, b, "That draft does not exist.", nil)
			}
			if err != nil {
				return err
			}
			if err := a.Store.SetSetting(ctx, "active", id); err != nil {
				return err
			}
			d.ResetView()
			if err := a.Store.Save(ctx, &d); err != nil {
				return err
			}
			return a.render(ctx, b, &d)
		case "/setchannel":
			if len(fields) != 2 {
				return a.reply(ctx, b, "Use /setchannel @channelname or /setchannel -1001234567890. Add the bot as an administrator with permission to post first.", nil)
			}
			return a.setChannel(ctx, b, fields[1])
		case "/unsetchannel":
			if err := a.Store.SetSetting(ctx, "channel", 0); err != nil {
				return err
			}
			return a.reply(ctx, b, "Channel publishing disabled for future posts.", nil)
		case "/preview", "/publish", "/replace", "/undo", "/download", "/done":
			d, err := a.target(ctx, b, m)
			if err != nil || d.ID == 0 {
				return err
			}
			action := strings.TrimPrefix(command, "/")
			if action == "done" {
				action = "back"
			} // compatibility; composition has no Done step
			return a.action(ctx, b, &d, action)
		default:
			// Leading slash can also be Markdown/body text. Only unknown bot commands
			// (as identified by Telegram) are treated as commands.
			for _, entity := range m.Entities {
				if entity.Type == models.MessageEntityTypeBotCommand && entity.Offset == 0 {
					return a.reply(ctx, b, "Unknown command. Use /help.", nil)
				}
			}
		}
	}
	d, err := a.target(ctx, b, m)
	if err != nil || d.ID == 0 {
		return err
	}
	if m.ID <= d.LastMessageID {
		return nil
	}
	if m.Text == "" {
		return a.notice(ctx, b, &d, "Send the post as text, using Markdown for the body.")
	}
	source := post.Source{MessageID: m.ID, UpdateID: updateID, EditDate: m.EditDate, Text: m.Text}
	if d.Number != 0 {
		return a.notice(ctx, b, &d, "This post is locked. Use /newpost for another draft.")
	}
	if d.View == "replace" || d.Step == post.Compose {
		a.refreshChoices(ctx, b, &d)
		if err := d.Replace(source); err != nil {
			if d.View == "replace" {
				d.ReplacementSource = &source
			} else {
				source.Full = true
				d.Sources = []post.Source{source}
			}
			d.LastMessageID = m.ID
			return a.notice(ctx, b, &d, err.Error())
		}
	} else if err := d.Append(source); err != nil {
		if d.Invalid == "" {
			d.Notice = err.Error()
		}
	}
	d.LastMessageID = m.ID
	if err := a.Store.Save(ctx, &d); err != nil {
		return err
	}
	return a.render(ctx, b, &d)
}

func (a *App) edited(ctx context.Context, b *bot.Bot, m *models.Message, updateID int64) error {
	d, err := a.Store.FromMessage(ctx, m.ID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if d.Number != 0 {
		return nil
	}
	full := d.ReplacementSource != nil && d.ReplacementSource.MessageID == m.ID
	for _, source := range d.Sources {
		full = full || source.MessageID == m.ID && source.Full
	}
	if full {
		a.refreshChoices(ctx, b, &d)
	}
	source := post.Source{MessageID: m.ID, UpdateID: updateID, EditDate: m.EditDate, Text: m.Text}
	if pending := d.ReplacementSource; pending != nil && pending.MessageID == m.ID {
		if !source.NewerThan(*pending) {
			return nil
		}
		d.ReplacementSource = &source
		if err := d.Replace(source); err != nil {
			d.Notice = err.Error()
		}
	} else if !d.EditSource(source) {
		return nil
	}
	if err := a.Store.Save(ctx, &d); err != nil {
		return err
	}
	// An edit belongs to this draft even if the owner is currently working on another.
	return a.render(ctx, b, &d)
}

func (a *App) target(ctx context.Context, b *bot.Bot, m *models.Message) (post.Draft, error) {
	var d post.Draft
	var err error
	if m.ReplyToMessage != nil {
		d, err = a.Store.FromMessage(ctx, m.ReplyToMessage.ID)
		if errors.Is(err, sql.ErrNoRows) {
			return post.Draft{}, a.reply(ctx, b, "That message is no longer linked to a draft. Reply to its current card or use /resume <id>.", nil)
		}
	} else {
		d, err = a.Store.Active(ctx)
	}
	if errors.Is(err, sql.ErrNoRows) {
		return post.Draft{}, a.reply(ctx, b, "Use /newpost or /resume <id> first.", nil)
	}
	return d, err
}

func (a *App) callback(ctx context.Context, b *bot.Bot, q *models.CallbackQuery) error {
	toast := func(text string) error {
		_, err := b.AnswerCallbackQuery(ctx, &bot.AnswerCallbackQueryParams{CallbackQueryID: q.ID, Text: text})
		return err
	}
	parts := strings.Split(q.Data, ":")
	if len(parts) != 4 || parts[0] != "draft" {
		return toast("This button is no longer valid.")
	}
	id, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return toast("Invalid draft button.")
	}
	d, err := a.Store.Get(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return toast("That draft no longer exists.")
	}
	if err != nil {
		return err
	}
	if parts[2] != strconv.FormatInt(d.UpdatedAt.UnixNano(), 10) || q.Message.Message.ID != d.CardID {
		old := q.Message.Message
		if old.ID != d.CardID && old.ID > 0 && old.From != nil && old.From.ID == b.ID() {
			a.deleteCard(ctx, b, old.ID)
		}
		if err := toast("The draft changed. Showing its current controls."); err != nil {
			return err
		}
		return a.prompt(ctx, b, d)
	}
	if err := toast(""); err != nil {
		return err
	}
	return a.action(ctx, b, &d, parts[3])
}

func (a *App) action(ctx context.Context, b *bot.Bot, d *post.Draft, action string) error {
	switch action {
	case "cancel":
		return a.discard(ctx, b, d.ID, 0)
	case "publish":
		return a.publish(ctx, b, d, false)
	case "retry":
		return a.publish(ctx, b, d, true)
	case "download":
		_, err := sendDocument(ctx, b, a.OwnerID, *d)
		return err
	case "preview":
		if err := d.Validate(); err != nil {
			return a.notice(ctx, b, d, err.Error())
		}
		d.Preview, d.View, d.Notice = true, "preview", ""
	case "back":
		if d.View == "preview" {
			d.Preview = false
		}
		d.ResetView()
		d.Notice, d.ReplacementSource = "", nil
	default:
		if d.Number != 0 {
			return a.notice(ctx, b, d, "Published posts are locked.")
		}
		switch {
		case action == "options":
			d.View, d.Notice = "options", ""
		case action == "replace":
			d.View, d.Notice = "replace", ""
			// Selecting Replace explicitly chooses where the next unthreaded
			// post message goes; replies and other cards remain independent.
			if err := a.Store.SetSetting(ctx, "active", d.ID); err != nil {
				return err
			}
		case action == "undo":
			if err := d.Undo(); err != nil {
				return a.notice(ctx, b, d, err.Error())
			}
		case action == "recover-pending" && d.PendingContent != "":
			d.Content, d.PendingContent, d.Sources, d.BaseContent = d.PendingContent, "", nil, ""
			d.Step, d.Notice, d.Invalid = post.Review, "", ""
			d.ResetView()
		case action == "clear-tags":
			d.Tags, d.View, d.Notice = []string{}, "tags-0", ""
		case strings.HasPrefix(action, "categories-") || strings.HasPrefix(action, "tags-"):
			a.refreshChoices(ctx, b, d)
			d.View, d.Notice = action, ""
		case strings.HasPrefix(action, "category-"):
			i, err := strconv.Atoi(strings.TrimPrefix(action, "category-"))
			if err != nil || i < 0 || i >= len(d.Categories) {
				return a.notice(ctx, b, d, "That category is no longer available.")
			}
			d.Category, d.Notice = d.Categories[i], ""
			d.ResetView()
		case strings.HasPrefix(action, "tag-"):
			i, err := strconv.Atoi(strings.TrimPrefix(action, "tag-"))
			if err != nil || i < 0 || i >= len(d.AvailableTags) {
				return a.notice(ctx, b, d, "That tag is no longer available.")
			}
			tag := d.AvailableTags[i]
			if slices.Contains(d.Tags, tag) {
				d.Tags = slices.DeleteFunc(d.Tags, func(t string) bool { return t == tag })
			} else {
				d.Tags = append(d.Tags, tag)
			}
			d.View, d.Notice = fmt.Sprintf("tags-%d", i/choicesPerPage), ""
		default:
			return a.notice(ctx, b, d, "Use the current draft controls.")
		}
	}
	if err := a.Store.Save(ctx, d); err != nil {
		return err
	}
	return a.render(ctx, b, d)
}

func (a *App) discard(ctx context.Context, b *bot.Bot, id int64, commandMessageID int) error {
	d, err := a.Store.Delete(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		if commandMessageID != 0 {
			return a.reply(ctx, b, fmt.Sprintf("Draft %d does not exist. Use /drafts to see saved drafts.", id), nil)
		}
		return nil
	}
	if errors.Is(err, store.ErrPublicationLocked) {
		return a.reply(ctx, b, "Only unfinished drafts can be deleted. This post is already prepared for publication.", nil)
	}
	if err != nil {
		return err
	}
	if d.CardID != 0 {
		if !a.deleteCard(ctx, b, d.CardID) {
			// An expired Telegram card may remain even though the draft is gone.
			_, err := b.EditMessageText(ctx, &bot.EditMessageTextParams{ChatID: a.OwnerID, MessageID: d.CardID, Text: fmt.Sprintf("Draft %d deleted.", d.ID), ReplyMarkup: emptyKeyboard()})
			if err != nil {
				a.logError(b, "deleted draft card", err)
			}
		}
	}
	if commandMessageID != 0 {
		ok, err := b.SetMessageReaction(ctx, &bot.SetMessageReactionParams{
			ChatID: a.OwnerID, MessageID: commandMessageID,
			Reaction: []models.ReactionType{{Type: models.ReactionTypeTypeEmoji, ReactionTypeEmoji: &models.ReactionTypeEmoji{Emoji: "👍"}}},
		})
		if err != nil || !ok {
			// Deletion already succeeded. A feedback failure must not report it
			// as failed or recreate the draft through the generic error handler.
			if err != nil {
				a.logError(b, "delete reaction", err)
			}
			if err := a.reply(ctx, b, fmt.Sprintf("Draft %d deleted.", id), nil); err != nil {
				a.logError(b, "delete confirmation", err)
			}
		}
	}
	return nil
}

func (a *App) notice(ctx context.Context, b *bot.Bot, d *post.Draft, text string) error {
	d.Notice = text
	if err := a.Store.Save(ctx, d); err != nil {
		return err
	}
	return a.render(ctx, b, d)
}

func (a *App) list(ctx context.Context, b *bot.Bot) error {
	drafts, err := a.Store.List(ctx)
	if err != nil {
		return err
	}
	lines := []string{"Saved drafts · /resume <id>"}
	for _, d := range drafts {
		title := clip(d.Title, 55)
		if title == "" {
			title = "Untitled"
		}
		status := "draft"
		if d.Number != 0 {
			status = fmt.Sprintf("post %d", d.Number)
		}
		lines = append(lines, fmt.Sprintf("%d · %s · %s", d.ID, title, status))
	}
	if len(drafts) == 0 {
		lines = []string{"No drafts yet. Use /newpost."}
	}
	return a.reply(ctx, b, strings.Join(lines, "\n"), nil)
}

func (a *App) setChannel(ctx context.Context, b *bot.Bot, target string) error {
	var chatID any = target
	if !strings.HasPrefix(target, "@") {
		id, err := strconv.ParseInt(target, 10, 64)
		if err != nil || id >= 0 {
			return a.reply(ctx, b, "Use a channel @username or negative numeric ID.", nil)
		}
		chatID = id
	}
	chat, err := b.GetChat(ctx, &bot.GetChatParams{ChatID: chatID})
	if err != nil {
		return a.reply(ctx, b, "Cannot access that channel. Add the bot as an administrator with permission to post, then try again.", nil)
	}
	if chat.Type != models.ChatTypeChannel {
		return a.reply(ctx, b, "The destination must be a channel.", nil)
	}
	if err := a.checkChannel(ctx, b, chat.ID); err != nil {
		return a.reply(ctx, b, err.Error(), nil)
	}
	if err := a.Store.SetSetting(ctx, "channel", chat.ID); err != nil {
		return err
	}
	return a.reply(ctx, b, "Channel set to "+chat.Title+" for future posts.", nil)
}

func (a *App) checkChannel(ctx context.Context, b *bot.Bot, id int64) error {
	member, err := b.GetChatMember(ctx, &bot.GetChatMemberParams{ChatID: id, UserID: b.ID()})
	if err != nil || member == nil || member.Type != models.ChatMemberTypeAdministrator || member.Administrator == nil || !member.Administrator.CanPostMessages {
		return errors.New("the bot must be a channel administrator with permission to post")
	}
	member, err = b.GetChatMember(ctx, &bot.GetChatMemberParams{ChatID: id, UserID: a.OwnerID})
	if err != nil || member == nil || !slices.Contains([]models.ChatMemberType{models.ChatMemberTypeOwner, models.ChatMemberTypeAdministrator, models.ChatMemberTypeMember}, member.Type) {
		return errors.New("you must be a member of the destination channel")
	}
	return nil
}
