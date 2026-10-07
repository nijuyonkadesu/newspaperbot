package telegram

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"html"
	"log"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"newspaperbot/internal/linkpreview"
	"newspaperbot/internal/metadata"
	"newspaperbot/internal/post"
	"newspaperbot/internal/store"
)

type App struct {
	mu             sync.Mutex
	taxonomyMu     sync.Mutex
	previewJobs    map[previewTarget]*previewJob
	previewWG      sync.WaitGroup
	ownerPreview   atomic.Pointer[previewJob]
	OwnerID        int64
	Store          *store.Store
	Metadata       metadata.Loader
	OutputDir      string
	WriteFile      func(string, []byte) error
	Repository     Repository
	Previews       *linkpreview.Client
	PreviewContext context.Context
}

type Repository interface {
	Catalog(context.Context) (metadata.Catalog, error)
	Refresh(context.Context) error
	Publish(context.Context, *store.Publication, func() error) error
	Articles(context.Context) ([]post.Article, error)
}

func (a *App) catalog(ctx context.Context) (metadata.Catalog, error) {
	if a.Repository != nil {
		return a.Repository.Catalog(ctx)
	}
	return a.Metadata.Taxonomy(ctx)
}

const help = `Write a post in one message:

Title

Short summary.

Markdown body.

/newpost — start a draft; optionally include the post
Edit your source message; new messages append. One card updates in place.

/taxonomy — copy categories and grouped tags; pin the list
Optional footer: Category: name, then Tags: tag1, tag2 (or -).

/drafts · /resume <draft number> — list / resume drafts
/posts — browse published articles; reply with a number to jump
/edit <article number> — edit / resume article
/save — save edits; keep article number, date, and URL
/replace — replace entire post
/undo — remove last appended text; keep chat message
/remove · /remove <message ID> — remove addition by reply / ID
/download [article number] — download selected post / published article
/publish — publish draft
/cancel — delete draft / discard article edits
/delete <draft number> — delete specific draft
/channels — show configured destination
/setchannel <@name or ID> — set destination for future posts
/unsetchannel — clear destination for future posts
/help — show usage

Preview, Category, and Tags are on the card.
Original message opens a quote; tap it to find the source.
Save changes updates Git and linked channel messages.
Reply to a card/source to append there; unthreaded text goes to the selected post.
Drafts and article edits survive restarts separately.
Undo doesn't revert edits, replacements, or taxonomy.
Deleted sources: /remove <message ID>.`

func (a *App) Handle(ctx context.Context, b *bot.Bot, update *models.Update) {
	// Cancel before acquiring mu: an enrichment edit may already be in flight.
	// Read-only commands such as /download don't interrupt a pending preview.
	if q := update.CallbackQuery; q != nil {
		parts := strings.Split(q.Data, ":")
		action := parts[len(parts)-1]
		if q.From.ID == a.OwnerID && a.privateChat(q.Message.Message) && action != "download" && !strings.HasPrefix(action, "source-") {
			a.cancelOwnerPreview()
		}
	} else if a.allowed(update.EditedMessage) {
		a.cancelOwnerPreview()
	} else if a.allowed(update.Message) {
		fields := strings.Fields(update.Message.Text)
		if len(fields) > 0 {
			command := strings.SplitN(fields[0], "@", 2)[0]
			switch command {
			case "/newpost", "/resume", "/posts", "/edit", "/cancel", "/delete", "/replace", "/save", "/publish", "/undo", "/remove":
				a.cancelOwnerPreview()
			default:
				if !strings.HasPrefix(command, "/") {
					a.cancelOwnerPreview()
				}
			}
		}
	}
	a.mu.Lock()
	defer a.mu.Unlock()
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
		fields := strings.Fields(m.Text)
		if len(fields) > 1 && strings.SplitN(fields[0], "@", 2)[0] == "/download" {
			return post.Draft{}, sql.ErrNoRows
		}
		if m.ReplyToMessage != nil {
			return a.Store.FromMessage(ctx, m.ReplyToMessage.ID)
		}
		if len(fields) > 0 {
			switch strings.SplitN(fields[0], "@", 2)[0] {
			case "/start", "/help", "/taxonomy", "/drafts", "/posts", "/edit", "/channels", "/setchannel", "/unsetchannel", "/delete", "/cancel", "/remove":
				return post.Draft{}, sql.ErrNoRows
			case "/resume":
				if len(fields) == 2 {
					slot, err := strconv.ParseInt(fields[1], 10, 64)
					if err == nil {
						return a.Store.GetBySlot(ctx, slot)
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

func (a *App) replyHTML(ctx context.Context, b *bot.Bot, text string) error {
	_, err := b.SendMessage(ctx, &bot.SendMessageParams{ChatID: a.OwnerID, Text: text, ParseMode: models.ParseModeHTML, DisableNotification: true})
	return err
}

func (a *App) message(ctx context.Context, b *bot.Bot, m *models.Message, updateID int64) error {
	if handled, err := a.articleListReply(ctx, b, m); handled {
		return err
	}
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
				catalog, err := a.catalog(ctx)
				if err != nil {
					return fmt.Errorf("new draft metadata: %w", err)
				}
				d, err = a.Store.New(ctx, catalog.Categories, catalog.Tags)
				if err != nil {
					return err
				}
				d.Portfolio = a.Repository != nil
				d.TagGroups = catalog.Groups
			}
			d.View, d.Notice = "", ""
			if len(fields) > 1 {
				text, issue := importText(m)
				a.refreshChoices(ctx, b, &d)
				if err := d.Replace(post.Source{MessageID: m.ID, UpdateID: updateID, Text: text}); err != nil {
					d.Notice = err.Error()
					d.Sources = []post.Source{{MessageID: m.ID, UpdateID: updateID, Text: text, Full: true}}
				}
				setMessageIssue(&d, m.ID, updateID, m.EditDate, issue)
			}
			d.LastMessageID = m.ID
			if err := a.Store.Save(ctx, &d); err != nil {
				return err
			}
			return a.render(ctx, b, &d)
		case "/cancel", "/delete":
			if command == "/cancel" && len(fields) != 1 || command == "/delete" && len(fields) != 2 {
				return a.reply(ctx, b, "Use /cancel for the active draft or /delete <draft number>.", nil)
			}
			id, err := a.Store.Setting(ctx, "active")
			if err != nil {
				return err
			}
			if len(fields) == 2 {
				slot, parseErr := strconv.ParseInt(fields[1], 10, 64)
				if parseErr != nil || slot <= 0 {
					return a.reply(ctx, b, "Use a positive draft number from /drafts.", nil)
				}
				d, loadErr := a.Store.GetBySlot(ctx, slot)
				if errors.Is(loadErr, sql.ErrNoRows) {
					return a.reply(ctx, b, fmt.Sprintf("Draft %d does not exist. Use /drafts to see saved drafts.", slot), nil)
				}
				if loadErr != nil {
					return loadErr
				}
				id = d.ID
			} else if m.ReplyToMessage != nil {
				d, err := a.target(ctx, b, m)
				if err != nil || d.ID == 0 {
					return err
				}
				id = d.ID
			}
			if id == 0 {
				return nil
			}
			if command == "/cancel" {
				d, err := a.Store.Get(ctx, id)
				if err != nil {
					return err
				}
				if d.Revision != nil {
					if err := a.discardChanges(ctx, b, &d, false); err != nil {
						return err
					}
					a.deleteOwnerMessage(ctx, b, m.ID, "cancel revision")
					return nil
				}
			}
			return a.discard(ctx, b, id, m.ID)
		case "/remove":
			const usage = "Reply with /remove · deleted source: /remove ID"
			messageID := 0
			if m.ReplyToMessage != nil && len(fields) == 1 {
				messageID = m.ReplyToMessage.ID
			} else if m.ReplyToMessage == nil && len(fields) == 2 {
				var parseErr error
				messageID, parseErr = strconv.Atoi(fields[1])
				if parseErr != nil || messageID <= 0 {
					return a.commandNotice(ctx, b, m, usage)
				}
			} else {
				return a.commandNotice(ctx, b, m, usage)
			}
			d, err := a.Store.FromMessage(ctx, messageID)
			if errors.Is(err, sql.ErrNoRows) {
				return a.commandNotice(ctx, b, m, "Source unavailable · not in an unfinished draft")
			}
			if err != nil {
				return err
			}
			if messageID == d.SourceReplyID {
				messageID = d.SourceReplyToID
			}
			if err := d.RemoveSource(messageID); err != nil {
				if err := a.notice(ctx, b, &d, err.Error()); err != nil {
					return err
				}
				a.deleteOwnerMessage(ctx, b, m.ID, "remove command")
				return nil
			}
			if err := a.Store.Save(ctx, &d); err != nil {
				return err
			}
			if err := a.render(ctx, b, &d); err != nil {
				return err
			}
			a.deleteOwnerMessage(ctx, b, messageID, "removed source")
			a.deleteOwnerMessage(ctx, b, m.ID, "remove command")
			return nil
		case "/drafts":
			return a.list(ctx, b)
		case "/posts":
			return a.articles(ctx, b, 0, "", true)
		case "/edit":
			if len(fields) != 2 {
				return a.reply(ctx, b, "Use /edit <article number> from /posts.", nil)
			}
			number, err := strconv.ParseInt(fields[1], 10, 64)
			if err != nil || number <= 0 {
				return a.reply(ctx, b, "Use an article number from /posts.", nil)
			}
			return a.editArticle(ctx, b, number)
		case "/channels":
			return a.channels(ctx, b)
		case "/resume":
			if len(fields) != 2 {
				return a.reply(ctx, b, "Use /resume <draft number> from /drafts.", nil)
			}
			slot, err := strconv.ParseInt(fields[1], 10, 64)
			if err != nil || slot <= 0 {
				return a.reply(ctx, b, "Use a positive draft number from /drafts.", nil)
			}
			d, err := a.Store.GetBySlot(ctx, slot)
			if errors.Is(err, sql.ErrNoRows) {
				return a.reply(ctx, b, "That draft does not exist.", nil)
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
			return a.render(ctx, b, &d)
		case "/setchannel":
			if len(fields) != 2 {
				return a.reply(ctx, b, "Use /setchannel @name or /setchannel -1001234567890. Add the bot as an administrator first.", nil)
			}
			return a.setChannel(ctx, b, fields[1])
		case "/unsetchannel":
			if err := a.Store.SetSetting(ctx, "channel", 0); err != nil {
				return err
			}
			return a.reply(ctx, b, "Publishing destination cleared for future posts.", nil)
		case "/download":
			if len(fields) > 1 {
				number, err := strconv.ParseInt(fields[1], 10, 64)
				if len(fields) != 2 || err != nil || number <= 0 {
					return a.replyHTML(ctx, b, "<code>/download</code> · selected post\n<code>/download 269</code> · published article")
				}
				return a.downloadArticle(ctx, b, number)
			}
			fallthrough
		case "/publish", "/save", "/replace", "/undo":
			d, err := a.target(ctx, b, m)
			if err != nil || d.ID == 0 {
				return err
			}
			action := strings.TrimPrefix(command, "/")
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
	if d.Locked() {
		if d.Number != 0 && d.Revision == nil {
			return a.notice(ctx, b, &d, fmt.Sprintf("Live article · use /edit %d to change it", d.Number))
		}
		return a.notice(ctx, b, &d, "Saving in progress · content is locked")
	}
	text, issue := importText(m)
	if text == "" {
		if issue == "" {
			return nil
		}
		setMessageIssue(&d, m.ID, updateID, m.EditDate, issue)
		d.LastMessageID = m.ID
		if err := a.Store.Save(ctx, &d); err != nil {
			return err
		}
		return a.render(ctx, b, &d)
	}
	source := post.Source{MessageID: m.ID, UpdateID: updateID, EditDate: m.EditDate, Text: text}
	a.refreshChoices(ctx, b, &d)
	if d.View == "replace" || d.Step == post.Compose {
		if err := d.Replace(source); err != nil {
			if d.View == "replace" {
				d.ReplacementSource = &source
			} else {
				source.Full = true
				d.Sources = []post.Source{source}
			}
			setMessageIssue(&d, m.ID, updateID, m.EditDate, issue)
			d.LastMessageID = m.ID
			return a.notice(ctx, b, &d, err.Error())
		}
	} else if err := d.Append(source); err != nil {
		if d.Invalid == "" {
			d.Notice = err.Error()
		}
	}
	setMessageIssue(&d, m.ID, updateID, m.EditDate, issue)
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
	if d.Locked() {
		return nil
	}
	version := post.Source{MessageID: m.ID, UpdateID: updateID, EditDate: m.EditDate}
	if !messageEditNewer(d, version) {
		return nil
	}
	text, issue := importText(m)
	if text == "" {
		if issue == "" {
			return nil
		}
		setMessageIssue(&d, m.ID, updateID, m.EditDate, issue)
		if err := a.Store.Save(ctx, &d); err != nil {
			return err
		}
		return a.render(ctx, b, &d)
	}
	a.refreshChoices(ctx, b, &d)
	source := post.Source{MessageID: m.ID, UpdateID: updateID, EditDate: m.EditDate, Text: text}
	if pending := d.ReplacementSource; pending != nil && pending.MessageID == m.ID {
		if !source.NewerThan(*pending) {
			return nil
		}
		d.ReplacementSource = &source
		if err := d.Replace(source); err != nil {
			d.Notice = err.Error()
		}
	} else if !d.EditSource(source) {
		if !hasMessageIssue(d, m.ID) {
			return nil
		}
		if d.View == "replace" || d.Step == post.Compose {
			err = d.Replace(source)
			if err != nil {
				if d.View == "replace" {
					d.ReplacementSource = &source
				} else {
					source.Full = true
					d.Sources = []post.Source{source}
				}
			}
		} else {
			err = d.Append(source)
		}
		if err != nil {
			d.Notice = err.Error()
		}
	}
	setMessageIssue(&d, m.ID, updateID, m.EditDate, issue)
	if err := a.Store.Save(ctx, &d); err != nil {
		return err
	}
	// An edit belongs to this draft even if the owner is currently working on another.
	return a.render(ctx, b, &d)
}

func setMessageIssue(d *post.Draft, messageID int, updateID int64, editDate int, reason string) {
	d.MessageIssues = slices.DeleteFunc(d.MessageIssues, func(issue post.MessageIssue) bool { return issue.MessageID == messageID })
	for _, source := range d.Sources {
		if source.MessageID == messageID && !source.Full && source.ParseError != "" {
			if reason != "" {
				reason += " · "
			}
			reason += "Invalid footer"
		}
	}
	if reason != "" {
		d.MessageIssues = append(d.MessageIssues, post.MessageIssue{MessageID: messageID, UpdateID: updateID, EditDate: editDate, Reason: reason})
	}
}

func hasMessageIssue(d post.Draft, messageID int) bool {
	return slices.ContainsFunc(d.MessageIssues, func(issue post.MessageIssue) bool { return issue.MessageID == messageID })
}

func messageEditNewer(d post.Draft, incoming post.Source) bool {
	for _, source := range d.Sources {
		if source.MessageID == incoming.MessageID && !incoming.NewerThan(source) {
			return false
		}
	}
	if source := d.ReplacementSource; source != nil && source.MessageID == incoming.MessageID && !incoming.NewerThan(*source) {
		return false
	}
	for _, issue := range d.MessageIssues {
		if issue.MessageID == incoming.MessageID && !incoming.NewerThan(post.Source{UpdateID: issue.UpdateID, EditDate: issue.EditDate}) {
			return false
		}
	}
	return true
}

func (a *App) target(ctx context.Context, b *bot.Bot, m *models.Message) (post.Draft, error) {
	var d post.Draft
	var err error
	if m.ReplyToMessage != nil {
		d, err = a.Store.FromMessage(ctx, m.ReplyToMessage.ID)
		if errors.Is(err, sql.ErrNoRows) {
			return post.Draft{}, a.reply(ctx, b, "That message is no longer linked to a draft. Reply to its current card or use /resume <number>.", nil)
		}
	} else {
		d, err = a.Store.Active(ctx)
	}
	if errors.Is(err, sql.ErrNoRows) {
		return post.Draft{}, a.reply(ctx, b, "Use /newpost or /resume <number> first.", nil)
	}
	return d, err
}

func (a *App) callback(ctx context.Context, b *bot.Bot, q *models.CallbackQuery) error {
	if handled, err := a.articleCallback(ctx, b, q); handled {
		return err
	}
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
	if strings.HasPrefix(action, "source-") {
		id, err := strconv.Atoi(strings.TrimPrefix(action, "source-"))
		if err != nil {
			return a.notice(ctx, b, d, "Source unavailable · use the current controls")
		}
		return a.showSource(ctx, b, d, id)
	}
	switch action {
	case "cancel":
		if d.Revision != nil {
			return a.discardChanges(ctx, b, d, false)
		}
		return a.discard(ctx, b, d.ID, 0)
	case "reload":
		return a.discardChanges(ctx, b, d, true)
	case "edit":
		return a.editArticle(ctx, b, d.Number)
	case "save":
		return a.saveChanges(ctx, b, d)
	case "publish":
		if d.Revision != nil {
			return a.notice(ctx, b, d, "Use Save changes for this live article.")
		}
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
		a.refreshChoices(ctx, b, d)
		d.Preview, d.View, d.Notice = true, "preview", ""
	case "back":
		if d.View == "preview" {
			d.Preview = false
		}
		d.ResetView()
		d.Notice, d.ReplacementSource = "", nil
	default:
		if d.Locked() {
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
			d.OrderTags()
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
			return a.reply(ctx, b, "That draft no longer exists. Use /drafts to see saved drafts.", nil)
		}
		return nil
	}
	if errors.Is(err, store.ErrPublicationLocked) {
		return a.reply(ctx, b, "Only unfinished drafts can be deleted. This post is already prepared for publication.", nil)
	}
	if err != nil {
		return err
	}
	a.deleteOwnerMessage(ctx, b, d.SourceReplyID, "source reply cleanup")
	if d.CardID != 0 {
		if !a.deleteCard(ctx, b, d.CardID) {
			// An expired Telegram card may remain even though the draft is gone.
			_, err := b.EditMessageText(ctx, &bot.EditMessageTextParams{ChatID: a.OwnerID, MessageID: d.CardID, Text: fmt.Sprintf("Draft %d deleted.", d.Slot), ReplyMarkup: emptyKeyboard()})
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
			if err := a.reply(ctx, b, fmt.Sprintf("Draft %d deleted.", d.Slot), nil); err != nil {
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

func (a *App) commandNotice(ctx context.Context, b *bot.Bot, m *models.Message, text string) error {
	var d post.Draft
	var err error
	if m.ReplyToMessage != nil {
		d, err = a.Store.FromMessage(ctx, m.ReplyToMessage.ID)
	}
	if d.ID == 0 && (err == nil || errors.Is(err, sql.ErrNoRows)) {
		d, err = a.Store.Active(ctx)
	}
	if err == nil {
		err = a.notice(ctx, b, &d, text)
	} else if errors.Is(err, sql.ErrNoRows) {
		err = a.reply(ctx, b, text, nil)
	}
	if err != nil {
		return err
	}
	a.deleteOwnerMessage(ctx, b, m.ID, "command cleanup")
	return nil
}

func (a *App) deleteOwnerMessage(ctx context.Context, b *bot.Bot, messageID int, label string) {
	if messageID == 0 {
		return
	}
	if _, err := b.DeleteMessage(ctx, &bot.DeleteMessageParams{ChatID: a.OwnerID, MessageID: messageID}); err != nil {
		a.logError(b, label, err)
	}
}

func (a *App) list(ctx context.Context, b *bot.Bot) error {
	drafts, err := a.Store.List(ctx)
	if err != nil {
		return err
	}
	lines := []string{"Saved drafts · /resume <number>"}
	for _, d := range drafts {
		title := clip(d.Title, 55)
		if title == "" {
			title = "Untitled"
		}
		lines = append(lines, fmt.Sprintf("%d · %s", d.Slot, title))
	}
	if len(drafts) == 0 {
		lines = []string{"No drafts yet. Use /newpost."}
	}
	return a.reply(ctx, b, strings.Join(lines, "\n"), nil)
}

func (a *App) channels(ctx context.Context, b *bot.Bot) error {
	id, err := a.Store.Setting(ctx, "channel")
	if err != nil {
		return err
	}
	if id == 0 {
		return a.reply(ctx, b, "No publishing destination configured. Use /setchannel.", nil)
	}
	chat, err := b.GetChat(ctx, &bot.GetChatParams{ChatID: id})
	if err != nil {
		return a.replyHTML(ctx, b, fmt.Sprintf("<b>Destination unavailable</b>\n<code>%d</code>", id))
	}
	heading := "Groups"
	if chat.Type == models.ChatTypeChannel {
		heading = "Channels"
	}
	parts := []string{"<b>" + html.EscapeString(chat.Title) + "</b>"}
	if chat.Username != "" {
		parts = append(parts, "@"+html.EscapeString(chat.Username))
	}
	parts = append(parts, fmt.Sprintf("<code>%d</code>", chat.ID))
	return a.replyHTML(ctx, b, "<b>"+heading+"</b>\n"+strings.Join(parts, " · "))
}

func (a *App) setChannel(ctx context.Context, b *bot.Bot, target string) error {
	var chatID any = target
	if !strings.HasPrefix(target, "@") {
		id, err := strconv.ParseInt(target, 10, 64)
		if err != nil || id >= 0 {
			return a.reply(ctx, b, "Use a channel or group @username or negative numeric ID.", nil)
		}
		chatID = id
	}
	chat, err := b.GetChat(ctx, &bot.GetChatParams{ChatID: chatID})
	if err != nil {
		return a.reply(ctx, b, "Cannot access that destination. Add the bot as an administrator, then try again.", nil)
	}
	if err := a.validateDestination(ctx, b, chat); err != nil {
		return a.reply(ctx, b, err.Error(), nil)
	}
	if err := a.Store.SetSetting(ctx, "channel", chat.ID); err != nil {
		return err
	}
	return a.reply(ctx, b, "Publishing destination set to "+chat.Title+".", nil)
}

func (a *App) checkDestination(ctx context.Context, b *bot.Bot, id int64) error {
	chat, err := b.GetChat(ctx, &bot.GetChatParams{ChatID: id})
	if err != nil {
		return errors.New("cannot access the configured publishing destination")
	}
	return a.validateDestination(ctx, b, chat)
}

func (a *App) validateDestination(ctx context.Context, b *bot.Bot, chat *models.ChatFullInfo) error {
	if !slices.Contains([]models.ChatType{models.ChatTypeChannel, models.ChatTypeGroup, models.ChatTypeSupergroup}, chat.Type) {
		return errors.New("the publishing destination must be a channel, group, or supergroup")
	}
	member, err := b.GetChatMember(ctx, &bot.GetChatMemberParams{ChatID: chat.ID, UserID: b.ID()})
	if err != nil || member == nil || member.Type != models.ChatMemberTypeAdministrator || member.Administrator == nil {
		return errors.New("the bot must be an administrator of the publishing destination")
	}
	if chat.Type == models.ChatTypeChannel && !member.Administrator.CanPostMessages {
		return errors.New("the bot must have permission to post messages in the channel")
	}
	member, err = b.GetChatMember(ctx, &bot.GetChatMemberParams{ChatID: chat.ID, UserID: a.OwnerID})
	if err != nil || !isChatMember(member) {
		return errors.New("you must be a member of the publishing destination")
	}
	return nil
}

func isChatMember(member *models.ChatMember) bool {
	if member == nil {
		return false
	}
	if slices.Contains([]models.ChatMemberType{models.ChatMemberTypeOwner, models.ChatMemberTypeAdministrator, models.ChatMemberTypeMember}, member.Type) {
		return true
	}
	return member.Type == models.ChatMemberTypeRestricted && member.Restricted != nil && member.Restricted.IsMember
}
