package telegram

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/go-telegram/bot/models"
	"newspaperbot/internal/post"
)

func exampleArticle(t *testing.T, number int64, date time.Time) post.Article {
	t.Helper()
	d := post.Draft{Title: "Original", Summary: "Summary", Content: "Body", Category: "concept", Tags: []string{"go"}, Slug: "original", PublishedAt: date}
	raw, err := d.PortfolioMarkdown()
	if err != nil {
		t.Fatal(err)
	}
	article, err := post.ReadArticle(fmt.Sprintf("content/tweets/%03d-original.md", number), string(raw))
	if err != nil {
		t.Fatal(err)
	}
	return article
}

func (h *harness) finishGit() {
	h.t.Helper()
	jobs, err := h.app.Store.PendingPublications(context.Background())
	if err != nil || len(jobs) != 1 {
		h.t.Fatal("missing queued save", err)
	}
	h.app.runPublication(context.Background(), h.bot, &jobs[0])
}

func (h *harness) clickPosts(label string) {
	h.t.Helper()
	listID, err := h.app.Store.Setting(context.Background(), "posts_message")
	if err != nil {
		h.t.Fatal(err)
	}
	h.api.mu.Lock()
	list := h.api.messages[int(listID)]
	h.api.mu.Unlock()
	for _, row := range list.Markup.InlineKeyboard {
		for _, button := range row {
			if button.Text != label {
				continue
			}
			h.app.Handle(context.Background(), h.bot, &models.Update{CallbackQuery: &models.CallbackQuery{
				ID: "articles", From: models.User{ID: 42}, Data: button.CallbackData,
				Message: models.MaybeInaccessibleMessage{Message: &models.Message{ID: int(listID), Chat: models.Chat{ID: 42, Type: models.ChatTypePrivate}}},
			}})
			return
		}
	}
	h.t.Fatalf("missing article list button %q", label)
}

func TestLiveArticlesAreDistinctAndRevisionsResumeWithoutNewCards(t *testing.T) {
	h := newHarness(t)
	old := exampleArticle(t, 268, time.Date(2020, 1, 2, 0, 0, 0, 0, time.UTC))
	today := exampleArticle(t, 269, time.Now().UTC())
	h.app.Repository = &fakeRepository{entries: []post.Article{today, old}}
	draft := h.ready("Unpublished body")
	h.send("/posts")
	listID, _ := h.app.Store.Setting(context.Background(), "posts_message")
	h.api.mu.Lock()
	text := h.api.messages[int(listID)].Text
	h.api.mu.Unlock()
	if !strings.Contains(text, "<b>Today</b>") || !strings.Contains(text, "<b>Earlier</b>") || !strings.Contains(text, "2020-01-02") || strings.Contains(text, "Unpublished body") {
		t.Fatal("list did not separate live articles and ages", text)
	}
	h.send("/posts")
	if len(h.api.live()) != 2 {
		t.Fatal("reissuing /posts left duplicate lists or deleted the draft card")
	}
	h.send("/edit 268")
	d := h.active()
	cardID := d.CardID
	if d.Slot != 0 || d.Revision == nil || d.Number != 268 {
		t.Fatal("article was treated as a draft")
	}
	h.click("preview")
	h.replyTo(cardID, "Revision addition")
	if d := h.active(); !d.Preview || !strings.Contains(d.Content, "Revision addition") {
		t.Fatal("revision preview/source routing failed")
	}
	h.api.mu.Lock()
	preview := h.api.messages[cardID].RichMarkdown
	h.api.mu.Unlock()
	if !strings.Contains(preview, "**Editing live**") || !strings.Contains(preview, "`#268`") || !strings.Contains(preview, "Earlier · 2020-01-02") {
		t.Fatal("preview lost live identity", preview)
	}
	h.restart()
	h.send("/edit 268")
	if h.active().CardID != cardID || !strings.Contains(h.active().Content, "Revision addition") {
		t.Fatal("restart/reopen discarded revision")
	}
	if list, err := h.app.Store.List(context.Background()); err != nil || len(list) != 1 || list[0].ID != draft.ID {
		t.Fatal("live article appeared in /drafts")
	}
	h.send("/publish")
	if jobs, _ := h.app.Store.PendingPublications(context.Background()); len(jobs) != 0 {
		t.Fatal("Publish queued an article revision")
	}
	h.click("save")
	h.finishGit()
	d = h.active()
	if d.Revision != nil || d.Number != 268 || d.PublishedAt.Format("2006-01-02") != "2020-01-02" || d.Content != "Body\n\nRevision addition" {
		t.Fatal("saving changed identity or lost content")
	}
	if !strings.Contains(d.Notice, "no linked channel message") || h.api.count("sendRichMessage", true) != 0 {
		t.Fatal("unassociated article created a channel message")
	}
	h.click("edit")
	h.replyTo(cardID, "Discard this addition")
	h.click("cancel")
	if h.draft(d.ID).Revision != nil || strings.Contains(h.draft(d.ID).Content, "Discard this") {
		t.Fatal("discard did not restore published version")
	}
	h.clickDraft(draft.ID, "preview")
	if h.draft(draft.ID).Content != "Unpublished body" {
		t.Fatal("revision crossed draft boundaries")
	}
}

func TestRevisionChannelUpdatesUseOriginalDestinationAndResumeParts(t *testing.T) {
	for _, document := range []bool{false, true} {
		t.Run(fmt.Sprintf("document=%t", document), func(t *testing.T) {
			h := newHarness(t)
			article := exampleArticle(t, 268, time.Date(2020, 1, 2, 0, 0, 0, 0, time.UTC))
			h.app.Repository = &fakeRepository{entries: []post.Article{article}}
			h.send("/edit 268")
			d := h.active()
			d.ChannelID, d.ContentMessageID, d.DocumentMode, d.Delivery = -1001, 500, document, "sent"
			if document {
				d.SummaryMessageID = 501
			}
			if err := h.app.Store.Save(context.Background(), &d); err != nil {
				t.Fatal(err)
			}
			h.api.mu.Lock()
			h.api.messages[500] = apiCall{ChatID: -1001, ResultID: 500}
			if document {
				h.api.messages[501] = apiCall{ChatID: -1001, ResultID: 501}
			}
			h.api.mu.Unlock()
			h.send("/setchannel @other")
			h.send("/replace")
			h.send("Changed title\n\nChanged summary\n\nChanged body\n\nCategory: research\nTags: new-tag")
			method := "editMessageText"
			if document {
				method = "editMessageMedia"
			}
			h.api.fail(method, 429)
			h.click("save")
			h.finishGit()
			d = h.active()
			if d.Revision == nil || !d.Revision.Applied || !strings.Contains(d.Notice, "channel update pending") {
				t.Fatal("channel failure lost progress")
			}
			if document && !d.Revision.SummaryDone {
				t.Fatal("summary success not checkpointed")
			}
			h.restart()
			h.api.fail(method, 0)
			h.click("save")
			d = h.active()
			if d.Revision != nil || d.ChannelID != -1001 || d.ContentMessageID != 500 || d.Delivery != "sent" || !strings.Contains(d.Notice, "channel updated") {
				t.Fatal("retry changed destination or recreated message")
			}
			if h.api.count("sendRichMessage", true) != 0 || h.api.count("sendDocument", true) != 0 || h.api.count("sendMessage", true) != 0 {
				t.Fatal("revision sent new channel messages")
			}
			h.api.mu.Lock()
			updated := h.api.messages[500]
			h.api.mu.Unlock()
			if document {
				if !strings.Contains(updated.Document, "Changed body") || !strings.Contains(updated.Document, `date: "2020-01-02"`) {
					t.Fatal("document not replaced", updated.Document)
				}
				if h.api.count("editMessageText", true) != 1 {
					t.Fatal("retry repeated successful summary edit")
				}
			} else if !strings.Contains(updated.RichMarkdown, "Changed body") {
				t.Fatal("rich channel post not updated")
			}
		})
	}
}

func TestRevisionMissingChannelMessageDoesNotBlockGit(t *testing.T) {
	h := newHarness(t)
	article := exampleArticle(t, 268, time.Date(2020, 1, 2, 0, 0, 0, 0, time.UTC))
	h.app.Repository = &fakeRepository{entries: []post.Article{article}}
	h.send("/edit 268")
	d := h.active()
	d.ChannelID, d.ContentMessageID, d.Delivery = -1001, 99999, "sent"
	if err := h.app.Store.Save(context.Background(), &d); err != nil {
		t.Fatal(err)
	}
	h.send("Addition")
	h.click("save")
	h.finishGit()
	d = h.active()
	if d.Revision != nil || !strings.Contains(d.Notice, "channel message unavailable") {
		t.Fatal("missing channel message blocked repository update", d.Notice)
	}
}

func TestRevisionConflictKeepsChangesAndOffersExplicitReload(t *testing.T) {
	h := newHarness(t)
	article := exampleArticle(t, 268, time.Date(2020, 1, 2, 0, 0, 0, 0, time.UTC))
	r := &fakeRepository{entries: []post.Article{article}, conflict: true}
	h.app.Repository = r
	h.send("/edit 268")
	h.send("Pending addition")
	h.click("save")
	h.finishGit()
	d := h.active()
	if !d.Revision.Conflict || !strings.Contains(d.Content, "Pending addition") {
		t.Fatal("conflict lost pending changes")
	}
	r.entries[0].Title = "Remote title"
	raw, _ := r.entries[0].PortfolioMarkdown()
	r.entries[0].Original = string(raw)
	h.click("reload")
	d = h.active()
	if d.Title != "Remote title" || strings.Contains(d.Content, "Pending addition") || d.Revision.Conflict || d.Locked() {
		t.Fatal("explicit reload did not load remote article")
	}
}

func TestNativeEditsToPublishedSourcesStayScopedToTheirRevision(t *testing.T) {
	h := newHarness(t)
	h.app.Repository = &fakeRepository{}
	live := h.ready("Original body")
	sourceID := h.messageID
	h.click("publish")
	h.finishGit()
	h.click("edit")
	h.click("preview")
	h.edit(sourceID, "Revised title\n\nSummary\n\nFirst revision")
	if d := h.draft(live.ID); d.Revision == nil || d.Content != "First revision" || d.CardID != live.CardID {
		t.Fatal("published source was not retained for explicit revision")
	}
	draft := h.ready("Separate unpublished body")
	h.edit(sourceID, "Revised title\n\nSummary\n\nSecond revision")
	if h.active().ID != draft.ID || h.draft(live.ID).Content != "Second revision" || h.draft(draft.ID).Content != "Separate unpublished body" {
		t.Fatal("native revision edit crossed into another draft")
	}
	h.restart()
	if err := h.app.RestoreCard(context.Background(), h.bot); err != nil {
		t.Fatal(err)
	}
	if len(h.api.live()) != 2 {
		t.Fatal("restart failed to restore inactive revision card in place")
	}
	h.clickDraft(live.ID, "save")
	h.finishGit()
	if h.active().ID != draft.ID || h.draft(live.ID).Content != "Second revision" || h.draft(live.ID).Revision != nil {
		t.Fatal("revision save stole selected draft or lost content")
	}
}

func TestArticleListPaginationEditsSameMessageAndSelectsArticle(t *testing.T) {
	h := newHarness(t)
	var entries []post.Article
	for number := int64(10); number > 0; number-- {
		entries = append(entries, exampleArticle(t, number, time.Date(2020, 1, int(number), 0, 0, 0, 0, time.UTC)))
	}
	h.app.Repository = &fakeRepository{entries: entries}
	h.send("/posts")
	listID, _ := h.app.Store.Setting(context.Background(), "posts_message")
	h.clickPosts("›")
	h.api.mu.Lock()
	list := h.api.messages[int(listID)]
	h.api.mu.Unlock()
	if !strings.Contains(list.Text, "<code>#2–#1</code>") || !strings.Contains(list.Text, "<code>#1</code>") || strings.Contains(list.Text, "<code>#10</code>") || len(h.api.live()) != 1 {
		t.Fatal("pagination recreated list or showed wrong page")
	}
	h.clickPosts("Edit #1")
	if d := h.active(); d.Number != 1 || d.Revision == nil || d.Slot != 0 {
		t.Fatal("article button selected wrong workspace")
	}
}

func TestArticlePreviewKeepsTheListAndActiveDraftSeparate(t *testing.T) {
	h := newHarness(t)
	h.app.Repository = &fakeRepository{entries: []post.Article{exampleArticle(t, 268, time.Date(2020, 1, 2, 0, 0, 0, 0, time.UTC))}}
	draft := h.ready("Draft body")
	h.send("/posts")
	listID, _ := h.app.Store.Setting(context.Background(), "posts_message")
	h.replyTo(int(listID), "268")
	sent := h.api.count("sendMessage", false)
	h.clickPosts("Preview #268")
	h.api.mu.Lock()
	preview := h.api.messages[int(listID)]
	h.api.mu.Unlock()
	if preview.RichMarkdown == "" || !strings.Contains(preview.RichMarkdown, "**Live** · `#268`") || strings.Contains(preview.RichMarkdown, "Editing live") || strings.Contains(preview.RichMarkdown, "\\*") || !strings.Contains(preview.RichMarkdown, "**Category:** concept") || !strings.Contains(preview.RichMarkdown, "**Tags:** go") {
		t.Fatal("article preview lost identity or showed draft/edit metadata", preview.RichMarkdown)
	}
	if revisions, err := h.app.Store.Revisions(context.Background()); err != nil || len(revisions) != 0 || h.active().ID != draft.ID || h.api.count("sendMessage", false) != sent {
		t.Fatal("preview opened a revision, changed selection, or sent another message", err)
	}
	h.send("Still drafting")
	if d := h.active(); d.ID != draft.ID || d.Content != "Draft body\n\nStill drafting" {
		t.Fatal("article preview hijacked unthreaded text")
	}
	h.clickPosts("Back")
	h.api.mu.Lock()
	list := h.api.messages[int(listID)]
	h.api.mu.Unlock()
	if list.RichMarkdown != "" || !strings.Contains(list.Text, "<code>#268–#268</code>") || h.api.count("sendMessage", false) != sent {
		t.Fatal("Back did not restore the selected range on the same message")
	}
	h.clickPosts("Preview #268")
	h.clickPosts("Edit #268")
	h.click("preview")
	if d := h.active(); d.Revision == nil || d.Number != 268 || !d.Preview || h.draft(draft.ID).Content != "Draft body\n\nStill drafting" {
		t.Fatal("Edit did not open a distinct revision with its own Preview button")
	}
}

func TestArticlePreviewFallbackRetainsControls(t *testing.T) {
	for _, tc := range []struct {
		name, content, notice, editError string
		reject                           bool
	}{
		{"rendering rejected", "**Body**", "Rich preview unavailable", "", true},
		{"rich API unavailable", "**Body**", "Rich preview unavailable", "Bad Request: message text is empty", false},
		{"too long", strings.Repeat("x", 32769), "Excerpt", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			article := exampleArticle(t, 268, time.Date(2020, 1, 2, 0, 0, 0, 0, time.UTC))
			article.Content = tc.content
			h.app.Repository = &fakeRepository{entries: []post.Article{article}}
			h.send("/posts")
			listID, _ := h.app.Store.Setting(context.Background(), "posts_message")
			h.api.mu.Lock()
			h.api.richRejected = tc.reject
			h.api.editError = tc.editError
			h.api.mu.Unlock()
			h.clickPosts("Preview #268")
			h.api.mu.Lock()
			preview := h.api.messages[int(listID)]
			h.api.mu.Unlock()
			if preview.RichMarkdown != "" || preview.ParseMode != "HTML" || !strings.Contains(preview.Text, tc.notice) || !strings.Contains(preview.Text, "<b>Live</b>") || len(h.api.live()) != 1 {
				t.Fatal("rejected rich preview hid the failure or lost its message")
			}
			if revisions, err := h.app.Store.Revisions(context.Background()); err != nil || len(revisions) != 0 {
				t.Fatal("fallback opened an editing session", err)
			}
			h.clickPosts("Back")
		})
	}
}

func TestPublishedCardPreviewDoesNotStartAnEdit(t *testing.T) {
	h := newHarness(t)
	h.app.Repository = &fakeRepository{}
	live := h.ready("Published body")
	h.click("publish")
	h.finishGit()
	draft := h.ready("Separate draft")
	h.clickDraft(live.ID, "preview")
	d := h.draft(live.ID)
	h.api.mu.Lock()
	preview := h.api.messages[d.CardID].RichMarkdown
	h.api.mu.Unlock()
	if d.Revision != nil || !d.Preview || d.CardID != live.CardID || h.active().ID != draft.ID || preview != previewMarkdown(d) || !strings.Contains(preview, "**Live**") || strings.Contains(preview, "Editing live") {
		t.Fatal("published preview started an edit or crossed into another draft", preview)
	}
	h.clickDraft(live.ID, "back")
	if d := h.draft(live.ID); d.Preview || d.View != "" || d.Revision != nil {
		t.Fatal("Back did not return to the published card")
	}
	h.clickDraft(live.ID, "edit")
	h.click("preview")
	if d := h.active(); d.Revision == nil || !d.Preview {
		t.Fatal("published article lost its edit Preview button")
	}
}

func TestArticleListRepliesReuseMessageAndCleanUp(t *testing.T) {
	h := newHarness(t)
	r := &fakeRepository{}
	for number := int64(1005); number >= 986; number-- {
		r.entries = append(r.entries, exampleArticle(t, number, time.Date(2020, 1, int(number-985), 0, 0, 0, 0, time.UTC)))
	}
	h.app.Repository = r
	draft := h.ready("Separate draft")
	h.send("/posts")
	listID, _ := h.app.Store.Setting(context.Background(), "posts_message")
	sent := h.api.count("sendMessage", false)
	for _, tc := range []struct{ reply, span string }{
		{"1002", "#1002–#995"},
		{"987", "#987–#986"},
		{"999", "#999–#992"},
	} {
		h.replyTo(int(listID), tc.reply)
		h.api.mu.Lock()
		list := h.api.messages[int(listID)]
		h.api.mu.Unlock()
		if !strings.Contains(list.Text, "<code>"+tc.span+"</code>") || h.api.count("sendMessage", false) != sent {
			t.Fatal("numeric reply showed the wrong range or resent the list", list.Text)
		}
		calls := h.api.snapshot()
		last := calls[len(calls)-1]
		if last.Method != "deleteMessage" || last.MessageID != h.messageID || calls[len(calls)-2].Method != "editMessageText" || calls[len(calls)-2].MessageID != int(listID) {
			t.Fatal("range reply was not deleted after updating the same message")
		}
		if d := h.active(); d.ID != draft.ID || d.Content != "Separate draft" {
			t.Fatal("article navigation altered the active draft")
		}
	}
	h.restart()
	for _, reply := range []string{"not a number", "0", "-1", "999999999999999999999999"} {
		h.replyTo(int(listID), reply)
		h.api.mu.Lock()
		list := h.api.messages[int(listID)]
		h.api.mu.Unlock()
		if !strings.Contains(list.Text, "<code>#999–#992</code>") || !strings.Contains(list.Text, "positive article number") {
			t.Fatal("invalid reply lost the selected range or lacked inline feedback", list.Text)
		}
	}
	h.replyTo(int(listID), "500")
	h.api.mu.Lock()
	list := h.api.messages[int(listID)]
	h.api.mu.Unlock()
	if !strings.Contains(list.Text, "<code>#999–#992</code>") || !strings.Contains(list.Text, "Article <code>#500</code> unavailable") {
		t.Fatal("missing article lost the selected range or lacked inline feedback", list.Text)
	}
	r.mu.Lock()
	r.entries = append([]post.Article{exampleArticle(t, 1006, time.Date(2020, 1, 21, 0, 0, 0, 0, time.UTC))}, r.entries...)
	for i := range r.entries {
		if r.entries[i].Number == 999 {
			r.entries[i].Title = "Updated on Git"
		}
	}
	r.mu.Unlock()
	h.clickPosts("Refresh")
	h.api.mu.Lock()
	list = h.api.messages[int(listID)]
	h.api.mu.Unlock()
	if !strings.Contains(list.Text, "<code>#999–#992</code>") || !strings.Contains(list.Text, "Updated on Git") || strings.Contains(list.Text, "unavailable") {
		t.Fatal("Refresh did not fetch changes or retain the selected article", list.Text)
	}
	h.send("/posts")
	listID, _ = h.app.Store.Setting(context.Background(), "posts_message")
	sent++
	r.mu.Lock()
	r.entries = append([]post.Article{exampleArticle(t, 1007, time.Date(2020, 1, 22, 0, 0, 0, 0, time.UTC))}, r.entries...)
	r.mu.Unlock()
	h.clickPosts("Refresh")
	h.api.mu.Lock()
	list = h.api.messages[int(listID)]
	h.api.mu.Unlock()
	if !strings.Contains(list.Text, "<code>#1007–#1000</code>") || h.api.count("sendMessage", false) != sent || len(h.api.live()) != 2 {
		t.Fatal("newest range did not refresh in place", list.Text)
	}
}

func TestPostsCommandReplacesOnlyItsList(t *testing.T) {
	for _, state := range []string{"present", "deleted", "cannot delete"} {
		t.Run(state, func(t *testing.T) {
			h := newHarness(t)
			h.app.Repository = &fakeRepository{entries: []post.Article{exampleArticle(t, 268, time.Date(2020, 1, 2, 0, 0, 0, 0, time.UTC))}}
			h.send("/taxonomy")
			h.send("/posts")
			taxonomyID, _ := h.app.Store.Setting(context.Background(), "taxonomy_message")
			oldID, _ := h.app.Store.Setting(context.Background(), "posts_message")
			h.restart()
			h.api.mu.Lock()
			if state == "deleted" {
				delete(h.api.messages, int(oldID))
			}
			h.api.deleteRejected = state == "cannot delete"
			h.api.mu.Unlock()
			sent := h.api.count("sendMessage", false)
			h.send("/posts")
			newID, err := h.app.Store.Setting(context.Background(), "posts_message")
			if err != nil || newID == 0 || newID == oldID || h.api.count("sendMessage", false) != sent+1 {
				t.Fatal("command did not create and track a fresh list", err)
			}
			h.api.mu.Lock()
			old, oldExists := h.api.messages[int(oldID)]
			_, taxonomyExists := h.api.messages[int(taxonomyID)]
			h.api.mu.Unlock()
			if state == "cannot delete" {
				if !oldExists || len(old.Markup.InlineKeyboard) != 0 {
					t.Fatal("undeletable list retained its controls")
				}
			} else if oldExists {
				t.Fatal("previous list was not deleted")
			}
			if !taxonomyExists {
				t.Fatal("replacing /posts removed the taxonomy list")
			}
			calls := len(h.api.snapshot())
			h.app.Handle(context.Background(), h.bot, &models.Update{CallbackQuery: &models.CallbackQuery{
				ID: "old-posts", From: models.User{ID: 42}, Data: "posts:268",
				Message: models.MaybeInaccessibleMessage{Message: &models.Message{ID: int(oldID), Chat: models.Chat{ID: 42, Type: models.ChatTypePrivate}}},
			}})
			if later := h.api.snapshot()[calls:]; len(later) != 1 || later[0].Method != "answerCallbackQuery" {
				t.Fatal("stale controls changed the current list")
			}
			h.send("/taxonomy")
			if id, _ := h.app.Store.Setting(context.Background(), "taxonomy_message"); id != taxonomyID || h.api.count("sendMessage", false) != sent+1 {
				t.Fatal("taxonomy no longer updates its pinned message in place")
			}
		})
	}
}

func TestRevisionReplyFooterUpdatesPreviewAndSurvivesRestart(t *testing.T) {
	h := newHarness(t)
	h.app.Repository = &fakeRepository{entries: []post.Article{exampleArticle(t, 268, time.Date(2020, 1, 2, 0, 0, 0, 0, time.UTC))}}
	h.send("/edit 268")
	h.click("preview")
	cardID := h.active().CardID
	h.replyTo(cardID, "New paragraph\nCategory: personal\nTags: invalid tag")
	invalidID := h.messageID
	text, markup := card(h.active())
	if h.active().Content != "Body" || h.active().Invalid == "" || !strings.Contains(text, "<b>Review</b>") || !strings.Contains(text, "Invalid footer") || !strings.Contains(text, fmt.Sprintf("<code>/remove %d</code>", invalidID)) || markup.InlineKeyboard[len(markup.InlineKeyboard)-1][0].Text != "Source 1" {
		t.Fatal("invalid footer lacked a linked recovery action on the same card", text)
	}
	h.send(fmt.Sprintf("/remove %d", invalidID))
	if h.active().Invalid != "" || len(h.active().MessageIssues) != 0 || h.active().Content != "Body" || !h.active().Preview {
		t.Fatal("removing the invalid addition lost content or preview state")
	}
	h.replyTo(cardID, "New paragraph\n\nCategory: research\nTags: new-tag, go")
	sourceID := h.messageID
	d := h.active()
	if d.Content != "Body\n\nNew paragraph" || d.Category != "research" || strings.Join(d.Tags, ",") != "new-tag,go" || d.CardID != cardID || !d.Preview {
		t.Fatal("revision reply failed to separate taxonomy from content or retain preview")
	}
	h.api.mu.Lock()
	preview := h.api.messages[cardID].RichMarkdown
	h.api.mu.Unlock()
	if !strings.Contains(preview, "**Category:** research\\*") || !strings.Contains(preview, "new\\-tag\\*") || strings.Contains(preview, "Category: research") {
		t.Fatal("preview did not show parsed metadata separately", preview)
	}
	h.restart()
	h.edit(sourceID, "Updated paragraph\npersonal\nsqlite")
	d = h.active()
	if d.Content != "Body\n\nUpdated paragraph" || d.Category != "personal" || strings.Join(d.Tags, ",") != "sqlite" || !d.Preview {
		t.Fatal("editing an appended reply lost its taxonomy or prior body after restart")
	}
	h.click("save")
	h.finishGit()
	d = h.active()
	if d.Revision != nil || d.Number != 268 || d.PublishedAt.Format("2006-01-02") != "2020-01-02" || d.Category != "personal" || strings.Join(d.Tags, ",") != "sqlite" || d.Content != "Body\n\nUpdated paragraph" {
		t.Fatal("saving failed to preserve article identity or appended taxonomy")
	}
}

func TestArticleListReplyIsRetainedWhenUpdateFails(t *testing.T) {
	h := newHarness(t)
	h.app.Repository = &fakeRepository{entries: []post.Article{exampleArticle(t, 268, time.Date(2020, 1, 2, 0, 0, 0, 0, time.UTC))}}
	h.send("/posts")
	listID, _ := h.app.Store.Setting(context.Background(), "posts_message")
	h.api.mu.Lock()
	h.api.editError = "temporary failure"
	h.api.mu.Unlock()
	h.replyTo(int(listID), "268")
	for _, call := range h.api.snapshot() {
		if call.Method == "deleteMessage" && call.MessageID == h.messageID {
			t.Fatal("failed list update deleted its input before successful handling")
		}
	}
	if id, _ := h.app.Store.Setting(context.Background(), "posts_message"); id != listID {
		t.Fatal("transient failure replaced the tracked list")
	}
}
