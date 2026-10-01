package telegram

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/go-telegram/bot/models"
	"newspaperbot/internal/post"
)

func (h *harness) click(action string) {
	h.t.Helper()
	d := h.active()
	h.clickDraft(d.ID, action)
}

func (h *harness) clickDraft(id int64, action string) {
	h.t.Helper()
	d := h.draft(id)
	h.api.mu.Lock()
	markup := h.api.messages[d.CardID].Markup
	h.api.mu.Unlock()
	for _, row := range markup.InlineKeyboard {
		for _, button := range row {
			if strings.HasSuffix(button.CallbackData, ":"+action) {
				h.callback(button.CallbackData)
				return
			}
		}
	}
	h.t.Fatalf("action %q is not visible in view %q", action, d.View)

}

func TestOneMessageDraftAndOneVisibleCard(t *testing.T) {
	h := newHarness(t)
	h.send("/newpost")
	initial := h.active().CardID
	h.send("Refreshing bot\n\nChecking if my bot works.\n\n## Special week\n\nUmamusume")
	d := h.active()
	if err := d.Validate(); err != nil {
		t.Fatal("single post message was not immediately publishable:", err)
	}
	if d.Category != "development" || len(d.Tags) != 0 {
		t.Fatal("unexpected taxonomy defaults")
	}
	live := h.api.live()
	if len(live) != 1 || live[0].ResultID != d.CardID || d.CardID != initial {
		t.Fatal("post submission replaced the original card")
	}
	if h.api.count("sendMessage", false) != 1 || h.api.count("deleteMessage", false) != 0 {
		t.Fatal("post submission sent or deleted another card")
	}
	if len(live[0].Markup.InlineKeyboard) != 2 || len(live[0].Markup.InlineKeyboard[0]) != 3 || len(live[0].Markup.InlineKeyboard[1]) != 2 {
		t.Fatal("ready controls do not show category and tags directly")
	}
	if !strings.Contains(live[0].Text, fmt.Sprintf("<code>#%d</code>", d.ID)) || strings.Contains(live[0].Text, fmt.Sprintf("Draft %d", d.ID)) {
		t.Fatal("overview did not use a compact inline draft ID")
	}
	h.click("publish")
	if !h.active().Exported || len(h.api.live()) != 1 {
		t.Fatal("one-tap publication failed or added a status message")
	}
}

func TestInlineNewPostAndNativeEditsAreSilent(t *testing.T) {
	h := newHarness(t)
	h.send("/newpost\nTitle\n\nSummary\n\nFirst body")
	root := h.messageID
	cardID := h.active().CardID
	if h.api.count("sendMessage", false) != 1 {
		t.Fatal("inline creation needed more than one bot message")
	}
	h.restart()
	h.edit(root, "/newpost\nNew title\n\nNew summary\n\nNew **body**")
	d := h.active()
	if d.Title != "New title" || d.Summary != "New summary" || d.Content != "New **body**" || d.CardID != cardID {
		t.Fatal("native edit did not update the same card after restart")
	}
	if h.api.count("sendMessage", false) != 1 || h.api.count("editMessageText", false) != 1 {
		t.Fatal("native edit added an acknowledgment")
	}
	h.send("Another paragraph")
	addition := h.messageID
	h.edit(addition, "Revised paragraph")
	if h.active().Content != "New **body**\n\nRevised paragraph" || len(h.api.live()) != 1 {
		t.Fatal("append editing duplicated content or cards")
	}
	h.send("/undo")
	if h.active().Content != "New **body**" || len(h.api.live()) != 1 {
		t.Fatal("undo failed or added a message")
	}
}

func TestInvalidNativeEditBlocksPublicationAndCanBeFixed(t *testing.T) {
	h := newHarness(t)
	h.ready("Original body")
	root := h.messageID
	h.edit(root, "Title only")
	h.send("/publish")
	if h.active().Content != "Original body" || h.active().Number != 0 || h.active().Invalid == "" {
		t.Fatal("invalid source edit discarded content or published the old snapshot")
	}
	h.restart()
	h.edit(root, "Fixed title\n\nFixed summary\n\nFixed body")
	if h.active().Invalid != "" || h.active().Content != "Fixed body" || len(h.api.live()) != 1 {
		t.Fatal("fixing the source did not recover quietly")
	}
	h.click("publish")
	if !h.active().Exported {
		t.Fatal("fixed source was not publishable")
	}
}

func TestIncompleteFirstMessageAndReplacementCanBeEdited(t *testing.T) {
	h := newHarness(t)
	h.send("/newpost")
	h.send("Title only")
	first := h.messageID
	h.restart()
	h.edit(first, "Title\n\nSummary\n\nOriginal body")
	if h.active().Content != "Original body" {
		t.Fatal("native correction of incomplete initial source was ignored")
	}
	h.send("/replace")
	h.send("Broken replacement")
	replacement := h.messageID
	if h.active().Content != "Original body" {
		t.Fatal("invalid replacement destroyed the prior post")
	}
	h.restart()
	h.edit(replacement, "Replacement title\n\nReplacement summary\n\nReplacement body")
	if h.active().Content != "Replacement body" || h.active().ReplacementSource != nil {
		t.Fatal("native correction of a pending replacement was ignored")
	}
	h.edit(first, "Old source\n\nSummary\n\nShould be ignored")
	if h.active().Content != "Replacement body" {
		t.Fatal("superseded source overwrote its replacement")
	}
	h.send("/replace")
	h.send("Another broken replacement")
	h.click("back")
	if h.active().ReplacementSource != nil || h.active().Content != "Replacement body" {
		t.Fatal("cancel replacement lost content or retained the pending binding")
	}
}

func TestEditsBelongToOriginalDraftAndRequireOwnerDM(t *testing.T) {
	h := newHarness(t)
	old := h.ready("Older body")
	root := h.messageID
	active := h.ready("Active body")
	h.edit(root, "Older title revised\n\nSummary\n\nOlder body revised")
	got, err := h.app.Store.Get(context.Background(), old.ID)
	if err != nil || got.Content != "Older body revised" || got.View != "" || h.active().ID != active.ID || h.active().Content != "Active body" {
		t.Fatal("editing an older source affected the active draft")
	}
	before := len(h.api.snapshot())
	for _, m := range []*models.Message{
		{ID: root, From: &models.User{ID: 99}, Chat: models.Chat{ID: 42, Type: models.ChatTypePrivate}, Text: "Attacker"},
		{ID: root, From: &models.User{ID: 42}, Chat: models.Chat{ID: -1001, Type: models.ChatTypeGroup}, Text: "Group"},
	} {
		h.app.Handle(context.Background(), h.bot, &models.Update{EditedMessage: m})
	}
	if len(h.api.snapshot()) != before {
		t.Fatal("unauthorized edit reached Telegram API")
	}
}

func TestMissingCardRecoveryAndStableUpdates(t *testing.T) {
	h := newHarness(t)
	h.ready("Body")
	oldID := h.active().CardID
	h.api.mu.Lock()
	delete(h.api.messages, oldID)
	h.api.mu.Unlock()
	h.send("/preview")
	if h.active().CardID == oldID || len(h.api.live()) != 1 {
		t.Fatal("deleted card was not recreated exactly once")
	}
	oldID = h.active().CardID
	deletes := h.api.count("deleteMessage", false)
	h.api.mu.Lock()
	h.api.deleteRejected = true
	h.api.mu.Unlock()
	h.send("An addition")
	if h.active().Content != "Body\n\nAn addition" {
		t.Fatal("cleanup failure undid the saved addition")
	}
	h.api.mu.Lock()
	old := h.api.messages[oldID]
	h.api.mu.Unlock()
	if old.ResultID != h.active().CardID || len(old.Markup.InlineKeyboard) == 0 || h.api.count("deleteMessage", false) != deletes {
		t.Fatal("new content replaced or deleted an existing card")
	}
}

func TestRenderedPreviewFallbackKeepsOneCard(t *testing.T) {
	h := newHarness(t)
	h.ready("**Body**")
	cardID := h.active().CardID
	h.api.mu.Lock()
	h.api.richRejected = true
	h.api.mu.Unlock()
	h.send("/preview")
	if h.active().CardID != cardID || len(h.api.live()) != 1 || h.api.count("sendDocument", false) != 0 || !strings.Contains(h.active().Notice, "/download") {
		t.Fatal("render rejection added noise or lost its source")
	}
	if !h.active().Preview || h.active().View != "preview" {
		t.Fatal("render rejection discarded the preferred preview mode")
	}
	h.api.mu.Lock()
	h.api.richRejected = false
	h.api.mu.Unlock()
	h.send("A correction")
	d := h.active()
	if d.CardID != cardID || h.api.live()[0].RichMarkdown != previewMarkdown(d) || d.Notice != "" {
		t.Fatal("content correction did not restore preview on the same card")
	}
}

func TestPreviewSurvivesContentUpdatesAndRestartOnTheSameCard(t *testing.T) {
	h := newHarness(t)
	h.send("/newpost")
	cardID := h.active().CardID
	h.send("Title\n\nSummary\n\nBody")
	root := h.messageID
	h.click("preview")
	assertPreview := func() {
		t.Helper()
		d := h.active()
		live := h.api.live()
		if !d.Preview || d.View != "preview" || d.CardID != cardID || len(live) != 1 || live[0].RichMarkdown != previewMarkdown(d) {
			t.Fatal("content update lost preview or replaced its card")
		}
		if !strings.Contains(live[0].RichMarkdown, fmt.Sprintf("`#%d`", d.ID)) || strings.Contains(live[0].RichMarkdown, fmt.Sprintf("Draft %d", d.ID)) {
			t.Fatal("preview did not use a compact inline draft ID")
		}
		if h.api.count("sendMessage", false) != 1 || h.api.count("sendRichMessage", false) != 0 || h.api.count("deleteMessage", false) != 0 {
			t.Fatal("editing content sent or deleted a card")
		}
	}
	h.send("Addition")
	addition := h.messageID
	assertPreview()
	h.edit(root, "Revised title\n\nRevised summary\n\nRevised body\n\nCategory: personal\nTags: go, sqlite")
	assertPreview()
	h.edit(addition, "Revised addition")
	assertPreview()
	h.send("/undo")
	assertPreview()
	h.edit(root, "Incomplete edit")
	if !h.active().Preview || h.active().Invalid == "" || h.active().CardID != cardID {
		t.Fatal("invalid source edit lost preview preference or valid saved content")
	}
	h.edit(root, "Fixed title\n\nFixed summary\n\nFixed body")
	assertPreview()
	// Simulate a draft saved before Preview became a separate preference.
	d := h.active()
	d.Preview = false
	if err := h.app.Store.Save(context.Background(), &d); err != nil {
		t.Fatal(err)
	}
	h.restart()
	if err := h.app.RestoreCard(context.Background(), h.bot); err != nil {
		t.Fatal(err)
	}
	assertPreview()
	h.send("After restart")
	assertPreview()
	h.click("categories-0")
	h.click("category-0")
	assertPreview()
	h.click("tags-0")
	h.click("tag-0")
	h.click("back")
	assertPreview()
	h.click("options")
	h.click("replace")
	h.send("Incomplete replacement")
	h.edit(h.messageID, "Replacement title\n\nReplacement summary\n\nReplacement body")
	assertPreview()
	h.click("back")
	h.send("Overview addition")
	if h.active().Preview || h.active().View != "" || h.active().CardID != cardID || h.api.live()[0].RichMarkdown != "" {
		t.Fatal("explicit Back did not switch subsequent updates to overview")
	}
}

func TestPreviewPreferenceIsIndependentForConcurrentDrafts(t *testing.T) {
	h := newHarness(t)
	a := h.ready("First body")
	rootA := h.messageID
	h.click("preview")
	b := h.ready("Second body")
	h.edit(rootA, "First revised\n\nSummary\n\nRevised body")
	h.replyTo(a.CardID, "First addition")
	h.send("Second addition")
	a, b = h.draft(a.ID), h.draft(b.ID)
	if !a.Preview || a.View != "preview" || b.Preview || b.View != "" || h.active().ID != b.ID {
		t.Fatal("one draft's mode/content update changed another draft's mode")
	}
	if len(h.api.live()) != 2 || h.api.count("sendMessage", false) != 2 || h.api.count("deleteMessage", false) != 0 {
		t.Fatal("concurrent edits moved or replaced cards")
	}
}

func TestOptionsPaginationAndMarkupBounds(t *testing.T) {
	h := newHarness(t)
	d := h.ready("Body")
	d.Categories, d.AvailableTags = make([]string, 8), make([]string, 20)
	for i := range d.Categories {
		d.Categories[i] = fmt.Sprintf("category-%d", i)
	}
	for i := range d.AvailableTags {
		d.AvailableTags[i] = fmt.Sprintf("tag-%d", i)
	}
	h.catalog(d.Categories, d.AvailableTags)
	d.Title, d.Summary, d.Content = "<title>&", strings.Repeat("🙂<&>", 500), strings.Repeat("<&>🙂", 2000)
	if err := h.app.Store.Save(context.Background(), &d); err != nil {
		t.Fatal(err)
	}
	if err := h.app.prompt(context.Background(), h.bot, d); err != nil {
		t.Fatal(err)
	}
	h.click("options")
	h.click("tags-0")
	h.click("tags-1")
	for _, call := range h.api.live() {
		if utf8.RuneCountInString(call.Text) > 4096 || strings.Contains(call.Text, "<title>") || call.ParseMode != "HTML" {
			t.Fatal("card was too long, unescaped, or unformatted")
		}
		if len(call.Markup.InlineKeyboard) > 6 {
			t.Fatal("taxonomy panel was not paginated")
		}
		for _, row := range call.Markup.InlineKeyboard {
			for _, button := range row {
				if len(button.CallbackData) > 64 {
					t.Fatal("callback exceeds Telegram's limit")
				}
			}
		}
	}
	h.click("tag-8")
	if len(h.active().Tags) != 1 || h.active().Tags[0] != d.AvailableTags[8] {
		t.Fatal("tag selection failed")
	}
	h.click("tag-8")
	if len(h.active().Tags) != 0 {
		t.Fatal("tag toggle failed")
	}
	h.click("back")
	h.click("options")
	h.click("categories-0")
	h.click("category-7")
	if h.active().Category != d.Categories[7] || h.active().View != "" {
		t.Fatal("category choice failed to return to the ready card")
	}
}

func TestLegacyDraftResumeAndPendingRecovery(t *testing.T) {
	h := newHarness(t)
	d := h.ready("Current body")
	d.ComposerVersion, d.Sources, d.Step, d.Editing, d.PendingContent = 0, nil, "summary", true, "Unfinished replacement"
	if err := h.app.Store.Save(context.Background(), &d); err != nil {
		t.Fatal(err)
	}
	h.restart()
	h.send(fmt.Sprintf("/resume %d", d.ID))
	if h.active().Content != "Current body" || h.active().Summary != "Summary" || h.active().PendingContent != "Unfinished replacement" || h.active().Step != post.Review {
		t.Fatal("legacy resume lost existing or pending content")
	}
	h.click("options")
	h.click("recover-pending")
	if h.active().Content != "Unfinished replacement" || h.active().PendingContent != "" {
		t.Fatal("legacy pending replacement was not recoverable")
	}
}

func TestMenuRegistersEveryTime(t *testing.T) {
	h := newHarness(t)
	for range 2 {
		if err := h.app.RegisterMenu(context.Background(), h.bot); err != nil {
			t.Fatal(err)
		}
	}
	if h.api.count("setMyCommands", false) != 2 || h.api.count("setChatMenuButton", false) != 2 {
		t.Fatal("registration did not apply on each startup invocation")
	}
}

func TestRestartRestoresCardAndReplacementView(t *testing.T) {
	h := newHarness(t)
	if err := h.app.RestoreCard(context.Background(), h.bot); err != nil || len(h.api.snapshot()) != 0 {
		t.Fatal("empty database created an unsolicited startup message")
	}
	h.ready("Body")
	h.send("/replace")
	cardID := h.active().CardID
	h.restart()
	if err := h.app.RestoreCard(context.Background(), h.bot); err != nil {
		t.Fatal(err)
	}
	if h.active().CardID != cardID || h.active().View != "replace" || len(h.api.live()) != 1 {
		t.Fatal("restart added a card or lost replacement state")
	}
	h.send("New title\n\nNew summary\n\nNew body")
	if h.active().Content != "New body" {
		t.Fatal("restored replacement became an append")
	}
}

func TestInvalidAdditionCanBeUndoneThroughOptions(t *testing.T) {
	h := newHarness(t)
	h.ready("Body")
	h.send("Addition")
	h.edit(h.messageID, " ")
	if h.active().Invalid == "" || h.active().Content != "Body\n\nAddition" {
		t.Fatal("invalid edit failed to preserve the prior body")
	}
	h.click("options")
	h.click("undo")
	if h.active().Invalid != "" || h.active().Content != "Body" || len(h.api.live()) != 1 {
		t.Fatal("options could not recover an invalid last addition")
	}
}

func TestCancelDeletesTheWholeDraft(t *testing.T) {
	for _, stage := range []string{"empty", "ready", "replacement"} {
		t.Run(stage, func(t *testing.T) {
			h := newHarness(t)
			h.send("/newpost")
			if stage != "empty" {
				h.send("Title\n\nSummary\n\nBody")
			}
			rootID := h.messageID
			if stage == "ready" {
				h.click("options")
			}
			if stage == "replacement" {
				h.send("/replace")
				h.send("Incomplete replacement")
			}
			before := h.active()
			h.click("cancel")
			active, err := h.app.Store.Setting(context.Background(), "active")
			if err != nil || active != 0 {
				t.Fatal("cancel did not clear the active draft")
			}
			if _, err := h.app.Store.Get(context.Background(), before.ID); !errors.Is(err, sql.ErrNoRows) {
				t.Fatal("cancelled draft still exists", err)
			}
			if len(h.api.live()) != 0 {
				t.Fatal("cancel left a card or added an acknowledgment")
			}
			h.restart()
			h.edit(rootID, "Resurrection\n\nSummary\n\nBody")
			h.send("/cancel")
			drafts, err := h.app.Store.List(context.Background())
			if err != nil || len(drafts) != 0 || len(h.api.live()) != 0 {
				t.Fatal("deleted draft reappeared after restart/edit/repeated cancel")
			}
		})
	}
}

func TestNewPostCancelLoopDoesNotAccumulateDraftsOrCards(t *testing.T) {
	h := newHarness(t)
	for range 5 {
		h.send("/newpost")
		first := h.active()
		h.send("/newpost")
		if h.active().ID != first.ID || h.active().CardID != first.CardID || len(h.api.live()) != 1 {
			t.Fatal("repeated newpost created another empty draft or card")
		}
		h.send("/cancel")
		drafts, err := h.app.Store.List(context.Background())
		if err != nil || len(drafts) != 0 || len(h.api.live()) != 0 {
			t.Fatal("newpost/cancel loop accumulated drafts or bot messages")
		}
	}
}

func TestDeleteByIDKeepsOtherDraftActive(t *testing.T) {
	h := newHarness(t)
	old := h.ready("Older body")
	active := h.ready("Active body")
	h.send(fmt.Sprintf("/delete %d", old.ID))
	if _, err := h.app.Store.Get(context.Background(), old.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("delete by ID left the draft")
	}
	if h.active().ID != active.ID || h.active().Content != "Active body" || len(h.api.live()) != 1 {
		t.Fatal("deleting another draft affected the active draft or added noise")
	}
	h.send(fmt.Sprintf("/delete %d", old.ID))
	if len(h.api.live()) != 2 || h.api.count("setMessageReaction", false) != 1 {
		t.Fatal("missing draft did not provide feedback or incorrectly reacted as success")
	}
	h.send("/delete")
	if len(h.api.live()) != 1 {
		t.Fatal("delete without ID did not discard the active draft")
	}
}

func TestDeleteReactionAndFallback(t *testing.T) {
	for _, rejectReaction := range []bool{false, true} {
		t.Run(fmt.Sprintf("reactionsRejected=%t", rejectReaction), func(t *testing.T) {
			h := newHarness(t)
			d := h.ready("Body")
			h.api.mu.Lock()
			h.api.reactionRejected = rejectReaction
			h.api.mu.Unlock()
			h.send(fmt.Sprintf("/delete %d", d.ID))
			if _, err := h.app.Store.Get(context.Background(), d.ID); !errors.Is(err, sql.ErrNoRows) {
				t.Fatal("deletion failed")
			}
			found := false
			for _, call := range h.api.snapshot() {
				if call.Method != "setMessageReaction" {
					continue
				}
				found = true
				if call.ChatID != 42 || call.MessageID != h.messageID || len(call.Reaction) != 1 || call.Reaction[0].ReactionTypeEmoji == nil || call.Reaction[0].ReactionTypeEmoji.Emoji != "👍" {
					t.Fatal("reaction did not target the delete command with a thumbs-up")
				}
			}
			if !found {
				t.Fatal("successful deletion had no reaction")
			}
			live := h.api.live()
			if rejectReaction {
				if len(live) != 1 || live[0].Text != fmt.Sprintf("Draft %d deleted.", d.ID) {
					t.Fatal("rejected reaction did not fall back to a truthful confirmation")
				}
			} else if len(live) != 0 {
				t.Fatal("success reaction added an unnecessary message")
			}
		})
	}
}

func TestDeleteWithoutActiveDraftProvidesFeedback(t *testing.T) {
	h := newHarness(t)
	h.send("/delete")
	live := h.api.live()
	if len(live) != 1 || !strings.Contains(live[0].Text, "No active draft") || h.api.count("setMessageReaction", false) != 0 {
		t.Fatal("delete without an active draft was silent or reported success")
	}
}

func TestDeleteDoesNotRemovePublicationRecovery(t *testing.T) {
	h := newHarness(t)
	h.ready("Body")
	h.app.WriteFile = func(string, []byte) error { return fmt.Errorf("disk full") }
	h.send("/publish")
	reserved := h.active()
	h.send("/cancel")
	h.send(fmt.Sprintf("/delete %d", reserved.ID))
	if h.active().Number != reserved.Number || h.active().Filename != reserved.Filename {
		t.Fatal("delete discarded a reserved publication's recovery state")
	}
}

func TestDeletedDraftStaleButtonsAndUnauthorizedDeletes(t *testing.T) {
	h := newHarness(t)
	d := h.ready("Body")
	stale := keyboard(d, "Publish", "publish").InlineKeyboard[0][0].CallbackData
	h.app.Handle(context.Background(), h.bot, &models.Update{Message: &models.Message{From: &models.User{ID: 99}, Chat: models.Chat{ID: 42, Type: models.ChatTypePrivate}, Text: fmt.Sprintf("/delete %d", d.ID)}})
	if h.active().ID != d.ID {
		t.Fatal("another user could delete the owner's draft")
	}
	h.send("/cancel")
	h.app.Handle(context.Background(), h.bot, &models.Update{CallbackQuery: &models.CallbackQuery{
		ID: "stale", From: models.User{ID: 42}, Data: stale,
		Message: models.MaybeInaccessibleMessage{Message: &models.Message{ID: d.CardID, From: &models.User{ID: 123, IsBot: true}, Chat: models.Chat{ID: 42, Type: models.ChatTypePrivate}}},
	}})
	drafts, err := h.app.Store.List(context.Background())
	if err != nil || len(drafts) != 0 || len(h.api.live()) != 0 {
		t.Fatal("a stale button recreated the deleted draft or added a message")
	}
}

func TestCancelWhenTelegramRefusesCardDeletion(t *testing.T) {
	h := newHarness(t)
	d := h.ready("Body")
	h.api.mu.Lock()
	h.api.deleteRejected = true
	h.api.mu.Unlock()
	h.send("/cancel")
	if _, err := h.app.Store.Get(context.Background(), d.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("Telegram cleanup failure prevented draft deletion")
	}
	live := h.api.live()
	if len(live) != 1 || live[0].Text != fmt.Sprintf("Draft %d deleted.", d.ID) || len(live[0].Markup.InlineKeyboard) != 0 {
		t.Fatal("undeletable card still showed draft content or controls")
	}
}
