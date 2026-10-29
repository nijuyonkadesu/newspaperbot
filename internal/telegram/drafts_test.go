package telegram

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

func (h *harness) draftList() (int, apiCall) {
	h.t.Helper()
	id, err := h.app.Store.Setting(context.Background(), "drafts_message")
	if err != nil || id == 0 {
		h.t.Fatal("draft list missing", err)
	}
	h.api.mu.Lock()
	defer h.api.mu.Unlock()
	return int(id), h.api.messages[int(id)]
}

func (h *harness) clickDraftList(label string) {
	h.t.Helper()
	id, list := h.draftList()
	for _, row := range list.Markup.InlineKeyboard {
		for _, button := range row {
			if button.Text == label {
				h.draftListQuery(id, button.CallbackData, 42)
				return
			}
		}
	}
	h.t.Fatalf("draft list button %q missing", label)
}

func (h *harness) draftListQuery(id int, data string, user int64) {
	h.t.Helper()
	h.app.Handle(context.Background(), h.bot, &models.Update{CallbackQuery: &models.CallbackQuery{
		ID: "draft-list", From: models.User{ID: user}, Data: data,
		Message: models.MaybeInaccessibleMessage{Message: &models.Message{ID: id, Chat: models.Chat{ID: 42, Type: models.ChatTypePrivate}}},
	}})
}

func TestDraftBrowserPreviewsWithoutSelectingAndOpensVisibleEditor(t *testing.T) {
	h := newHarness(t)
	first := h.ready("### First body\nCategory: development\nTags: go")
	root := h.messageID
	second := h.ready("Second body")
	h.send("/drafts")
	listID, list := h.draftList()
	if list.ParseMode != "HTML" || !strings.Contains(list.Text, "<b>Drafts</b>") || !strings.Contains(list.Text, "<code>#1</code>") || !strings.Contains(list.Text, "<code>#2</code> · <b>Selected</b>") {
		t.Fatal("draft list lacks formatted identities or selection", list.Text)
	}
	h.clickDraftList("Preview #1")
	_, preview := h.draftList()
	if !strings.Contains(preview.RichMarkdown, "### First body") || !strings.Contains(preview.RichMarkdown, "`#1`") || !strings.Contains(preview.RichMarkdown, "**Tags:** go") || h.active().ID != second.ID || h.draft(first.ID).CardID != first.CardID {
		t.Fatal("preview changed selection, source bindings, or rich formatting", preview.RichMarkdown)
	}
	h.clickDraftList("Back")
	h.clickDraftList("Edit #1")
	d := h.active()
	if d.ID != first.ID || d.CardID <= listID || d.CardID == first.CardID || d.Content != first.Content || !d.PublishedAt.IsZero() {
		t.Fatal("opening the draft failed to bring its unchanged editor into view")
	}
	h.api.mu.Lock()
	_, oldCard := h.api.messages[first.CardID]
	_, oldList := h.api.messages[listID]
	h.api.mu.Unlock()
	if oldCard || oldList {
		t.Fatal("opening the draft retained obsolete cards")
	}
	h.click("preview")
	card := h.active().CardID
	h.edit(root, "Revised title\n\nSummary\n\n### Edited first body\nCategory: development\nTags: go")
	if got := h.active(); got.CardID != card || !got.Preview || got.Content != "### Edited first body" || h.draft(second.ID).Content != second.Content {
		t.Fatal("moving the editor broke native edits, preview state, or another draft")
	}
	h.click("replace")
	if got := h.active(); got.Content != "### Edited first body" || got.View != "replace" {
		t.Fatal("opening Replace changed content before a replacement arrived")
	}
	h.send("Replacement title\n\nReplacement summary\n\nReplacement body")
	replacement := h.messageID
	h.edit(replacement, "Replacement title\n\nReplacement summary\n\nReplacement corrected")
	d = h.active()
	if d.Slot != first.Slot || d.CardID != card || !d.Preview || d.Content != "Replacement corrected" || !d.PublishedAt.IsZero() {
		t.Fatal("replacement/source edits changed draft identity, card, date, or preview state")
	}
}

func TestDraftBrowserPaginatesRepliesAndReplacesOnlyItsOwnList(t *testing.T) {
	h := newHarness(t)
	for range 10 {
		h.ready("Body")
	}
	active := h.active()
	h.send("/taxonomy")
	taxonomy, _ := h.app.Store.Setting(context.Background(), "taxonomy_message")
	h.send("/drafts")
	firstID, list := h.draftList()
	if len(list.Markup.InlineKeyboard) != listItemsPerPage+1 || strings.Contains(list.Text, "<code>#9</code>") {
		t.Fatal("draft list did not paginate")
	}
	h.clickDraftList("›")
	id, list := h.draftList()
	if id != firstID || !strings.Contains(list.Text, "<code>#9</code>") || !strings.Contains(list.Text, "<code>#10</code>") {
		t.Fatal("paging sent another message or omitted remaining drafts")
	}
	h.replyTo(id, "3")
	_, list = h.draftList()
	if !strings.Contains(list.Text, "<code>#3</code>") || h.active().ID != active.ID {
		t.Fatal("reply navigation failed or selected a draft")
	}
	if last := h.api.snapshot(); last[len(last)-1].Method != "deleteMessage" || last[len(last)-1].MessageID != h.messageID {
		t.Fatal("handled list reply was not cleaned up")
	}
	h.send("/drafts")
	newID, _ := h.draftList()
	h.api.mu.Lock()
	_, oldExists := h.api.messages[firstID]
	_, editorExists := h.api.messages[active.CardID]
	_, taxonomyExists := h.api.messages[int(taxonomy)]
	h.api.mu.Unlock()
	if newID == firstID || oldExists || !editorExists || !taxonomyExists {
		t.Fatal("reissuing /drafts failed to replace just its own list")
	}
}

func TestDraftBrowserRejectsDeletedAndRecycledDraftButtons(t *testing.T) {
	h := newHarness(t)
	old := h.ready("Old body")
	h.send("/drafts")
	id, list := h.draftList()
	data := list.Markup.InlineKeyboard[0][1].CallbackData
	h.send("/delete 1")
	replacement := h.ready("Replacement body")
	if replacement.Slot != old.Slot || replacement.ID == old.ID {
		t.Fatal("fixture did not recycle the visible number")
	}
	h.draftListQuery(id, data, 42)
	_, current := h.draftList()
	if h.active().ID != replacement.ID || h.active().CardID != replacement.CardID || !strings.Contains(current.Text, "Draft unavailable") {
		t.Fatal("a stale button opened a different draft with a recycled number")
	}
	h.send("/drafts")
	newID, _ := h.draftList()
	before := len(h.api.snapshot())
	h.draftListQuery(newID, fmt.Sprintf("draft-open:%d", replacement.ID), 99)
	if len(h.api.snapshot()) != before {
		t.Fatal("a stranger's draft browser callback received a response")
	}
	h.draftListQuery(id, data, 42)
	if h.active().CardID != replacement.CardID {
		t.Fatal("an obsolete browser message changed the editor")
	}
}

func TestResumeBringsEditorDownAndSourceEditsRemainInPlace(t *testing.T) {
	h := newHarness(t)
	d := h.ready("Body")
	root := h.messageID
	h.click("preview")
	h.send("/resume 1")
	resumed := h.active()
	if resumed.CardID == d.CardID || !resumed.Preview || !resumed.PublishedAt.IsZero() {
		t.Fatal("resume reused the invisible old card or changed draft state")
	}
	h.edit(root, "Title\n\nSummary\n\nUpdated body")
	if h.active().CardID != resumed.CardID || h.active().Content != "Updated body" {
		t.Fatal("native source editing moved the newly opened editor")
	}
}

func TestFailedDraftOpenRetainsExistingEditorAndList(t *testing.T) {
	h := newHarness(t)
	d := h.ready("Body")
	h.send("/drafts")
	listID, _ := h.draftList()
	failed := false
	b, err := bot.New("123:test", bot.WithSkipGetMe(), bot.WithServerURL("https://telegram.test"),
		bot.WithHTTPClient(time.Second, &http.Client{Transport: previewTransport(func(r *http.Request) (*http.Response, error) {
			if !failed && filepath.Base(r.URL.Path) == "sendMessage" {
				failed = true
				return nil, errors.New("temporary send failure")
			}
			return h.api.RoundTrip(r)
		})}))
	if err != nil {
		t.Fatal(err)
	}
	h.bot = b
	h.send("/resume 1")
	got := h.active()
	h.api.mu.Lock()
	_, editorExists := h.api.messages[d.CardID]
	_, listExists := h.api.messages[listID]
	h.api.mu.Unlock()
	if got.CardID != d.CardID || got.Content != d.Content || !editorExists || !listExists || !strings.Contains(got.Notice, "temporary send failure") {
		t.Fatal("a failed open discarded content, existing controls, or error feedback")
	}
}
