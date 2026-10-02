package telegram

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"newspaperbot/internal/metadata"
	"newspaperbot/internal/post"
	"newspaperbot/internal/store"
)

type apiCall struct {
	Method                                  string
	ChatID                                  int64
	MessageID, ResultID                     int
	Text, RichMarkdown, Document, ParseMode string
	Markup                                  models.InlineKeyboardMarkup
	Reaction                                []models.ReactionType
	ReplyParameters                         *models.ReplyParameters
	DisableNotification                     bool
}

type fakeAPI struct {
	mu               sync.Mutex
	calls            []apiCall
	failures         map[string]int
	messages         map[int]apiCall
	nextID           int
	ownerAbsent      bool
	botCannotPost    bool
	botNotAdmin      bool
	editError        string
	replyError       string
	deleteRejected   bool
	richRejected     bool
	reactionRejected bool
}

func (f *fakeAPI) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.Body != nil {
		defer r.Body.Close()
	}
	w := httptest.NewRecorder()
	f.serve(w, r)
	return w.Result(), nil
}

func (f *fakeAPI) serve(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(1 << 20); err != nil && err != http.ErrNotMultipart {
		http.Error(w, err.Error(), 400)
		return
	}
	if r.MultipartForm != nil {
		defer r.MultipartForm.RemoveAll()
	}
	call := apiCall{Method: filepath.Base(r.URL.Path), Text: r.FormValue("text"), ParseMode: r.FormValue("parse_mode")}
	call.ChatID, _ = strconv.ParseInt(r.FormValue("chat_id"), 10, 64)
	call.MessageID, _ = strconv.Atoi(r.FormValue("message_id"))
	call.DisableNotification = r.FormValue("disable_notification") == "true"
	if raw := r.FormValue("reply_parameters"); raw != "" {
		call.ReplyParameters = &models.ReplyParameters{}
		if err := json.Unmarshal([]byte(raw), call.ReplyParameters); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
	}
	if raw := r.FormValue("rich_message"); raw != "" {
		var rich models.InputRichMessage
		if err := json.Unmarshal([]byte(raw), &rich); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		call.RichMarkdown = rich.Markdown
	}
	if raw := r.FormValue("reply_markup"); raw != "" {
		_ = json.Unmarshal([]byte(raw), &call.Markup)
	}
	if raw := r.FormValue("reaction"); raw != "" {
		if err := json.Unmarshal([]byte(raw), &call.Reaction); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
	}
	if file, _, err := r.FormFile("document"); err == nil {
		data, err := io.ReadAll(file)
		file.Close()
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		call.Document = string(data)
	}
	if call.Method == "editMessageMedia" && r.MultipartForm != nil {
		for _, files := range r.MultipartForm.File {
			file, err := files[0].Open()
			if err != nil {
				http.Error(w, err.Error(), 500)
				return
			}
			data, err := io.ReadAll(file)
			file.Close()
			if err != nil {
				http.Error(w, err.Error(), 500)
				return
			}
			call.Document = string(data)
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, call)
	reject := func(code int, description string) {
		json.NewEncoder(w).Encode(map[string]any{"ok": false, "error_code": code, "description": description})
	}
	if code := f.failures[call.Method]; code != 0 && call.ChatID < 0 {
		reject(code, "test failure")
		return
	}
	if call.Method == "editMessageText" && f.editError != "" {
		text := f.editError
		f.editError = ""
		reject(400, text)
		return
	}
	if call.Method == "sendMessage" && call.ReplyParameters != nil && f.replyError != "" {
		reject(400, f.replyError)
		return
	}
	if call.Method == "deleteMessage" && f.deleteRejected {
		reject(400, "message can't be deleted")
		return
	}
	if call.Method == "setMessageReaction" && f.reactionRejected {
		reject(400, "reactions unavailable")
		return
	}
	if f.richRejected && call.ChatID > 0 && call.RichMarkdown != "" {
		reject(400, "can't parse rich Markdown")
		return
	}
	var result any
	switch call.Method {
	case "getChat":
		id, kind, username := int64(-1001), "channel", "first"
		switch r.FormValue("chat_id") {
		case "@other", "-2002":
			id = -2002
			username = "other"
		case "@group", "-3003":
			id = -3003
			kind = "supergroup"
			username = "group"
		}
		result = map[string]any{"id": id, "type": kind, "title": "Test destination", "username": username}
	case "getChatMember":
		status := "member"
		if r.FormValue("user_id") == "123" {
			status = "administrator"
			if f.botNotAdmin {
				status = "member"
			}
		} else if f.ownerAbsent {
			status = "left"
		}
		result = map[string]any{"status": status, "can_post_messages": !f.botCannotPost, "user": map[string]any{"id": 123, "is_bot": true, "first_name": "Test"}}
	case "answerCallbackQuery", "setMyCommands", "setChatMenuButton", "setMessageReaction":
		result = true
	case "deleteMessage":
		delete(f.messages, call.MessageID)
		result = true
	case "editMessageText", "editMessageReplyMarkup", "editMessageMedia":
		old, ok := f.messages[call.MessageID]
		if !ok {
			reject(400, "message to edit not found")
			return
		}
		if call.Method == "editMessageReplyMarkup" {
			id := call.MessageID
			old.Markup = call.Markup
			call = old
			call.MessageID = id
		}
		call.ResultID = call.MessageID
		f.messages[call.MessageID] = call
		result = map[string]any{"message_id": call.MessageID, "date": 1, "chat": map[string]any{"id": call.ChatID, "type": "private"}}
	default:
		f.nextID++
		call.ResultID = f.nextID
		if call.ChatID > 0 {
			f.messages[call.ResultID] = call
		}
		result = map[string]any{"message_id": call.ResultID, "date": 1, "chat": map[string]any{"id": call.ChatID, "type": "private"}}
	}
	f.calls[len(f.calls)-1].ResultID = call.ResultID
	json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": result})
}

func (f *fakeAPI) live() []apiCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	result := []apiCall{}
	for _, call := range f.messages {
		if call.ChatID > 0 {
			result = append(result, call)
		}
	}
	return result
}

func (f *fakeAPI) snapshot() []apiCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]apiCall(nil), f.calls...)
}
func (f *fakeAPI) fail(method string, code int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failures[method] = code
}
func (f *fakeAPI) count(method string, channelOnly bool) int {
	n := 0
	for _, call := range f.snapshot() {
		if call.Method == method && (!channelOnly || call.ChatID < 0) {
			n++
		}
	}
	return n
}

type harness struct {
	t         *testing.T
	app       *App
	bot       *bot.Bot
	api       *fakeAPI
	messageID int
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	api := &fakeAPI{failures: map[string]int{}, messages: map[int]apiCall{}, nextID: 10000}
	b, err := bot.New("123:test", bot.WithSkipGetMe(), bot.WithServerURL("https://telegram.test"), bot.WithHTTPClient(time.Second, &http.Client{Transport: api}))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	s, err := store.Open(filepath.Join(dir, "drafts.db"))
	if err != nil {
		t.Fatal(err)
	}
	app := &App{OwnerID: 42, Store: s, OutputDir: dir, WriteFile: post.WriteFile, Metadata: metadata.Loader{
		NumberSource: "../../testdata/blog-number.json", CategoriesSource: "../../testdata/categories.json", TagsSource: "../../testdata/tags.json",
	}}
	t.Cleanup(func() { app.Store.Close() })
	return &harness{t: t, app: app, bot: b, api: api}
}

func (h *harness) send(text string) {
	h.t.Helper()
	h.messageID++
	h.app.Handle(context.Background(), h.bot, &models.Update{ID: int64(h.messageID), Message: &models.Message{
		ID: h.messageID, From: &models.User{ID: 42}, Chat: models.Chat{ID: 42, Type: models.ChatTypePrivate}, Text: text,
	}})
}

func (h *harness) active() post.Draft {
	h.t.Helper()
	d, err := h.app.Store.Active(context.Background())
	if err != nil {
		h.t.Fatal(err)
	}
	return d
}

func (h *harness) restart() {
	h.t.Helper()
	if err := h.app.Store.Close(); err != nil {
		h.t.Fatal(err)
	}
	s, err := store.Open(filepath.Join(h.app.OutputDir, "drafts.db"))
	if err != nil {
		h.t.Fatal(err)
	}
	h.app.Store = s
}

func (h *harness) ready(content string) post.Draft {
	h.t.Helper()
	h.send("/newpost")
	h.send("Title\n\nSummary\n\n" + content)
	d := h.active()
	if err := d.Validate(); err != nil {
		h.t.Fatal(err)
	}
	return d
}

func (h *harness) callback(data string) {
	h.t.Helper()
	parts := strings.Split(data, ":")
	id, _ := strconv.ParseInt(parts[1], 10, 64)
	d := h.draft(id)
	h.app.Handle(context.Background(), h.bot, &models.Update{CallbackQuery: &models.CallbackQuery{
		ID: "callback", From: models.User{ID: 42}, Data: data,
		Message: models.MaybeInaccessibleMessage{Message: &models.Message{ID: d.CardID, From: &models.User{ID: 123, IsBot: true}, Chat: models.Chat{ID: 42, Type: models.ChatTypePrivate}}},
	}})
}

func TestOwnerDMRestriction(t *testing.T) {
	h := newHarness(t)
	for _, m := range []*models.Message{
		{From: &models.User{ID: 42}, Chat: models.Chat{ID: -1001, Type: models.ChatTypeGroup}, Text: "/newpost"},
		{From: &models.User{ID: 99}, Chat: models.Chat{ID: 99, Type: models.ChatTypePrivate}, Text: "/newpost"},
		{Chat: models.Chat{ID: 42, Type: models.ChatTypePrivate}, Text: "/newpost"},
	} {
		h.app.Handle(context.Background(), h.bot, &models.Update{Message: m})
	}
	for _, q := range []*models.CallbackQuery{
		{From: models.User{ID: 99}, Message: models.MaybeInaccessibleMessage{Message: &models.Message{From: &models.User{ID: 42}, Chat: models.Chat{ID: 42, Type: models.ChatTypePrivate}}}},
		{From: models.User{ID: 42}, Message: models.MaybeInaccessibleMessage{Message: &models.Message{From: &models.User{ID: 42}, Chat: models.Chat{ID: -1, Type: models.ChatTypeSupergroup}}}},
		{From: models.User{ID: 42}, InlineMessageID: "inline"},
	} {
		h.app.Handle(context.Background(), h.bot, &models.Update{CallbackQuery: q})
	}
	h.app.Handle(context.Background(), h.bot, &models.Update{ChannelPost: &models.Message{Text: "/newpost"}})
	if len(h.api.snapshot()) != 0 {
		t.Fatal("unauthorized update reached the API")
	}
	drafts, err := h.app.Store.List(context.Background())
	if err != nil || len(drafts) != 0 {
		t.Fatal("unauthorized update created a draft")
	}
	h.send("/newpost")
	if h.active().Step != post.Compose {
		t.Fatal("owner DM was not accepted")
	}
}

func TestRestartResumeEditAndStaleButtons(t *testing.T) {
	h := newHarness(t)
	d := h.ready("## First chunk")
	rootID := h.messageID
	originalNumber := d.Slot
	h.restart()
	h.send("Second chunk")
	if h.active().Content != "## First chunk\n\nSecond chunk" {
		t.Fatal("restart lost accumulated content")
	}
	h.edit(rootID, "New title\n\nNew summary\n\nFirst rewritten")
	if h.active().Content != "First rewritten\n\nSecond chunk" {
		t.Fatal("edit lost an appended paragraph")
	}
	h.send("/newpost")
	h.send(fmt.Sprintf("/resume %d", originalNumber))
	d = h.active()
	stale := keyboard(d, "Options", "options").InlineKeyboard[0][0].CallbackData
	h.send("Another paragraph")
	before := len(h.api.live())
	h.callback(stale)
	if len(h.api.live()) != before || h.active().View != "" {
		t.Fatal("stale callback changed draft or added noise")
	}
	h.send("/replace")
	h.send("Replacement title\n\nReplacement summary\n\nReplacement **Markdown**")
	if h.active().Content != "Replacement **Markdown**" || h.active().Step != post.Review {
		t.Fatal("replacement did not finish immediately")
	}
}

func TestLocalPublishAndPreview(t *testing.T) {
	h := newHarness(t)
	if err := os.WriteFile(filepath.Join(h.app.OutputDir, "43.md"), []byte("existing"), 0600); err != nil {
		t.Fatal(err)
	}
	d := h.ready("# Heading\n\n**bold** and `code`")
	h.send("/preview")
	calls := h.api.snapshot()
	if calls[len(calls)-1].RichMarkdown != previewMarkdown(d) {
		t.Fatal("preview did not pass Markdown to Telegram")
	}
	h.send("/publish")
	d = h.active()
	if !d.Exported || d.Number != 44 || d.ChannelID != 0 {
		t.Fatalf("bad publication: %+v", d)
	}
	expected, err := d.Markdown()
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(d.Filename)
	if err != nil || string(got) != string(expected) {
		t.Fatal("export differs from the draft")
	}
	h.send("/publish")
	if h.active().Number != 44 {
		t.Fatal("republishing allocated another number")
	}
}

func TestChannelConfigurationAndMembership(t *testing.T) {
	h := newHarness(t)
	h.send("/channels")
	if calls := h.api.snapshot(); calls[len(calls)-1].Text != "No publishing destination configured. Use /setchannel." {
		t.Fatal("empty destination list was unclear")
	}
	h.send("/setchannel @first")
	h.send("/setchannel @other")
	channel, err := h.app.Store.Setting(context.Background(), "channel")
	if err != nil || channel != -2002 {
		t.Fatal("channel replacement failed")
	}
	h.send("/channels")
	if calls := h.api.snapshot(); calls[len(calls)-1].ParseMode != "HTML" || calls[len(calls)-1].Text != "<b>Channels</b>\n<b>Test destination</b> · @other · <code>-2002</code>" {
		t.Fatal("configured channel was not grouped under Channels")
	}
	h.send("/setchannel @group")
	channel, err = h.app.Store.Setting(context.Background(), "channel")
	if err != nil || channel != -3003 {
		t.Fatal("supergroup destination was rejected")
	}
	h.send("/channels")
	if calls := h.api.snapshot(); calls[len(calls)-1].ParseMode != "HTML" || calls[len(calls)-1].Text != "<b>Groups</b>\n<b>Test destination</b> · @group · <code>-3003</code>" {
		t.Fatal("configured destination was not listed with its current status")
	}
	h.api.mu.Lock()
	h.api.ownerAbsent = true
	h.api.mu.Unlock()
	h.send("/setchannel @first")
	channel, err = h.app.Store.Setting(context.Background(), "channel")
	if err != nil || channel != -3003 {
		t.Fatal("invalid destination replaced the channel")
	}
	h.api.mu.Lock()
	h.api.ownerAbsent = false
	h.api.botCannotPost = true
	h.api.mu.Unlock()
	h.send("/setchannel @first")
	channel, err = h.app.Store.Setting(context.Background(), "channel")
	if err != nil || channel != -3003 {
		t.Fatal("bot without posting rights accepted")
	}
	h.api.mu.Lock()
	h.api.botCannotPost = false
	h.api.botNotAdmin = true
	h.api.mu.Unlock()
	h.send("/setchannel @group")
	channel, err = h.app.Store.Setting(context.Background(), "channel")
	if err != nil || channel != -3003 {
		t.Fatal("non-administrator bot replaced the destination")
	}
	h.send("/unsetchannel")
	channel, err = h.app.Store.Setting(context.Background(), "channel")
	if err != nil || channel != 0 {
		t.Fatal("channel was not unset")
	}
}

func TestUncertainDeliveryNeedsExplicitRetry(t *testing.T) {
	h := newHarness(t)
	h.send("/setchannel @first")
	h.ready("**Body**")
	h.api.fail("sendRichMessage", 500)
	h.send("/publish")
	d := h.active()
	if !d.Exported || d.Delivery != "uncertain" {
		t.Fatalf("lost failure state: %+v", d)
	}
	h.restart()
	for _, call := range h.api.snapshot() {
		if strings.Contains(call.Text, "Published to the channel.") {
			t.Fatal("failure reported as success")
		}
	}
	h.send("/publish")
	if h.api.count("sendRichMessage", true) != 1 {
		t.Fatal("uncertain delivery was automatically retried")
	}
	h.send("/setchannel @other")
	h.api.fail("sendRichMessage", 0)
	d = h.active()
	h.callback(keyboard(d, "Retry", "retry").InlineKeyboard[0][0].CallbackData)
	d = h.active()
	if d.Delivery != "sent" || d.ChannelID != -1001 || d.ContentMessageID == 0 {
		t.Fatal("retry failed or changed the frozen destination")
	}
	h.send("/publish")
	if h.api.count("sendRichMessage", true) != 2 {
		t.Fatal("confirmed publication was sent again")
	}
}

func TestDocumentFallbackResumesAfterConfirmedSummary(t *testing.T) {
	h := newHarness(t)
	h.send("/setchannel @first")
	h.ready("Body")
	h.api.fail("sendRichMessage", 400)
	h.api.fail("sendDocument", 403)
	h.send("/publish")
	d := h.active()
	if !d.Exported || !d.DocumentMode || d.Delivery != "pending" || d.SummaryMessageID == 0 {
		t.Fatalf("lost partial delivery: %+v", d)
	}
	h.api.fail("sendDocument", 0)
	h.send("/publish")
	if h.active().Delivery != "sent" || h.api.count("sendMessage", true) != 1 || h.api.count("sendRichMessage", true) != 1 || h.api.count("sendDocument", true) != 2 {
		t.Fatal("retry duplicated a confirmed part or failed to finish")
	}
}

func TestLongContentAndExportFailure(t *testing.T) {
	h := newHarness(t)
	d := h.ready(strings.Repeat("x", 32769))
	h.send("/preview")
	if h.api.count("sendRichMessage", false) != 0 || h.api.count("sendDocument", false) != 0 {
		t.Fatal("long preview added an unsolicited attachment")
	}
	h.send("/download")
	data, err := d.Markdown()
	if err != nil {
		t.Fatal(err)
	}
	calls := h.api.snapshot()
	if calls[len(calls)-1].Document != string(data) {
		t.Fatal("download changed the Markdown")
	}
	h.app.WriteFile = func(string, []byte) error { return fmt.Errorf("disk full") }
	h.send("/publish")
	d = h.active()
	if d.Exported || d.Number != 43 {
		t.Fatal("export failure lost reservation or was reported as exported")
	}
	h.app.WriteFile = post.WriteFile
	h.send("/publish")
	if !h.active().Exported || h.active().Number != 43 {
		t.Fatal("export retry changed the reserved number")
	}
}

func TestMetadataFailureDoesNotReserveOrExport(t *testing.T) {
	h := newHarness(t)
	h.ready("Body")
	h.app.Metadata.NumberSource = filepath.Join(h.app.OutputDir, "missing.json")
	h.send("/publish")
	d := h.active()
	if d.Number != 0 || d.Exported || d.Content != "Body" {
		t.Fatal("metadata failure lost draft or allocated a publication")
	}
}

func TestTaxonomyChangesRequireCorrection(t *testing.T) {
	h := newHarness(t)
	h.ready("Body")
	path := filepath.Join(h.app.OutputDir, "categories.json")
	if err := os.WriteFile(path, []byte(`["personal"]`), 0600); err != nil {
		t.Fatal(err)
	}
	h.app.Metadata.CategoriesSource = path
	h.send("/publish")
	d := h.active()
	if d.Number != 0 || d.Exported || len(d.Categories) != 1 || d.Categories[0] != "personal" {
		t.Fatal("taxonomy change was not reflected in the editable draft")
	}
	h.callback(keyboard(d, "Personal", "category-0").InlineKeyboard[0][0].CallbackData)
	h.send("/publish")
	if !h.active().Exported {
		t.Fatal("corrected taxonomy could not be published")
	}
}

func TestLongChannelPostUsesSummaryAndDocument(t *testing.T) {
	h := newHarness(t)
	h.send("/setchannel @first")
	h.ready(strings.Repeat("body\n", 7000))
	h.send("/publish")
	if h.active().Delivery != "sent" || h.api.count("sendRichMessage", true) != 0 || h.api.count("sendMessage", true) != 1 || h.api.count("sendDocument", true) != 1 {
		t.Fatal("long post did not publish its summary and full document")
	}
}

func TestRecoveryAfterFileWrittenBeforeCheckpoint(t *testing.T) {
	h := newHarness(t)
	d := h.ready("Body")
	d, err := h.app.Store.Reserve(context.Background(), d.ID, 42, 0, h.app.OutputDir)
	if err != nil {
		t.Fatal(err)
	}
	data, err := d.Markdown()
	if err != nil {
		t.Fatal(err)
	}
	if err := post.WriteFile(d.Filename, data); err != nil {
		t.Fatal(err)
	}
	h.restart()
	h.send("/publish")
	if !h.active().Exported || h.active().Number != 43 {
		t.Fatal("restart could not recover an already written file")
	}
}

func TestReplayedTextDoesNotAppendTwice(t *testing.T) {
	h := newHarness(t)
	h.ready("Body")
	h.send("Extra")
	h.restart()
	h.app.Handle(context.Background(), h.bot, &models.Update{Message: &models.Message{ID: h.messageID, From: &models.User{ID: 42}, Chat: models.Chat{ID: 42, Type: models.ChatTypePrivate}, Text: "Extra"}})
	if h.active().Content != "Body\n\nExtra" {
		t.Fatal("replayed content was duplicated")
	}
}

func (h *harness) edit(id int, text string) {
	h.t.Helper()
	h.messageID++
	h.app.Handle(context.Background(), h.bot, &models.Update{ID: int64(h.messageID), EditedMessage: &models.Message{ID: id, From: &models.User{ID: 42}, Chat: models.Chat{ID: 42, Type: models.ChatTypePrivate}, Text: text}})
}
