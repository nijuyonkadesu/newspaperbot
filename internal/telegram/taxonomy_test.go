package telegram

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/go-telegram/bot/models"
	"newspaperbot/internal/metadata"
	"newspaperbot/internal/post"
)

func (h *harness) draft(id int64) post.Draft {
	h.t.Helper()
	d, err := h.app.Store.Get(context.Background(), id)
	if err != nil {
		h.t.Fatal(err)
	}
	return d
}

func (h *harness) catalog(categories, tags []string) {
	h.t.Helper()
	for _, source := range []struct {
		path   string
		values []string
	}{
		{"categories.json", categories}, {"tags.json", tags},
	} {
		data, err := json.Marshal(source.values)
		if err != nil {
			h.t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(h.app.OutputDir, source.path), data, 0600); err != nil {
			h.t.Fatal(err)
		}
	}
	h.app.Metadata.CategoriesSource = filepath.Join(h.app.OutputDir, "categories.json")
	h.app.Metadata.TagsSource = filepath.Join(h.app.OutputDir, "tags.json")
}

func (h *harness) replyTo(id int, text string) {
	h.t.Helper()
	h.messageID++
	h.app.Handle(context.Background(), h.bot, &models.Update{ID: int64(h.messageID), Message: &models.Message{
		ID: h.messageID, From: &models.User{ID: 42}, Chat: models.Chat{ID: 42, Type: models.ChatTypePrivate}, Text: text,
		ReplyToMessage: &models.Message{ID: id},
	}})
}

func (h *harness) syncTaxonomy(requested bool) {
	h.t.Helper()
	if err := h.app.SyncTaxonomy(context.Background(), h.bot, requested); err != nil {
		h.t.Fatal(err)
	}
}

func TestTaxonomyTextPreservesCategoryTagGroups(t *testing.T) {
	text, err := taxonomyText(metadata.Catalog{
		Categories: []string{"personal", "empty", "concept", "CI"},
		Tags:       []string{"go", "sqlite", "shared", "unassigned"},
		Groups: map[string][]string{
			"concept":  {"sqlite", "shared"},
			"personal": {"go", "shared"},
			"empty":    {},
			"CI":       {"sqlite", "go"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	concept := "<b>2.</b> <code>concept</code>\n<code>shared</code> · <code>sqlite</code>"
	personal := "<b>4.</b> <code>personal</code>\n<code>go</code> · <code>shared</code>"
	if !strings.Contains(text, concept) || !strings.Contains(text, personal) {
		t.Fatal("category/tag relationships were flattened or not sorted")
	}
	if strings.Count(text, "<code>shared</code>") != 2 {
		t.Fatal("tag shared across categories was deduplicated")
	}
	if !strings.Contains(text, "<b>3.</b> <code>empty</code>\n<i>No observed tags</i>") || !strings.Contains(text, "<b>Other tags</b>\n<code>unassigned</code>") {
		t.Fatal("empty groups or unassigned tags were lost")
	}
	if !strings.Contains(text, "<b>1.</b> <code>CI</code>") || !strings.Contains(text, "<pre>Category: CI\nTags: go, sqlite</pre>") {
		t.Fatal("footer example did not use the category group")
	}
	if strings.Contains(text, "observed use") || strings.Contains(text, "Pin this message") {
		t.Fatal("taxonomy includes explanatory clutter")
	}
}

func TestTaxonomyListUpdatesSameMessageAndSurvivesRestart(t *testing.T) {
	h := newHarness(t)
	categories, tags := make([]string, 8), make([]string, 20)
	for i := range categories {
		categories[i] = fmt.Sprintf("category-%d", i)
	}
	for i := range tags {
		tags[i] = fmt.Sprintf("tag-%d", i)
	}
	categories[7] = "<&>"
	h.catalog(categories, tags)
	h.app.Metadata.NumberSource = "unavailable-number-source"
	h.syncTaxonomy(false)
	if len(h.api.live()) != 0 {
		t.Fatal("poll created an unsolicited list")
	}
	h.send("/taxonomy")
	id, err := h.app.Store.Setting(context.Background(), "taxonomy_message")
	if err != nil || id == 0 {
		t.Fatal("list message not tracked", err)
	}
	live := h.api.live()
	if len(live) != 1 || live[0].ParseMode != "HTML" || strings.Count(live[0].Text, "<code>") != 28 || strings.Count(live[0].Text, "<pre>") != 1 || !strings.Contains(live[0].Text, "<code>&lt;&amp;&gt;</code>") {
		t.Fatal("items not individually formatted or escaped")
	}
	h.syncTaxonomy(false)
	if h.api.count("editMessageText", false) != 0 {
		t.Fatal("unchanged source caused an API write")
	}
	h.restart()
	h.send("/taxonomy")
	if len(h.api.live()) != 1 || h.api.live()[0].ResultID != int(id) {
		t.Fatal("repeat command lost original message/pin")
	}
	tags = append(tags, "new-tag")
	h.catalog(categories, tags)
	h.syncTaxonomy(false)
	if len(h.api.live()) != 1 || h.api.live()[0].ResultID != int(id) || !strings.Contains(h.api.live()[0].Text, "<code>new-tag</code>") {
		t.Fatal("changed source did not edit tracked list")
	}
	before := h.api.live()[0].Text
	h.app.Metadata.TagsSource = "missing-source"
	if err := h.app.SyncTaxonomy(context.Background(), h.bot, false); err == nil {
		t.Fatal("source failure ignored")
	}
	if len(h.api.live()) != 1 || h.api.live()[0].Text != before {
		t.Fatal("source failure destroyed last valid list")
	}
}

func TestDeletedTaxonomyListIsRecreatedOnlyByCommand(t *testing.T) {
	h := newHarness(t)
	h.send("/taxonomy")
	id := h.api.live()[0].ResultID
	h.api.mu.Lock()
	delete(h.api.messages, id)
	h.api.mu.Unlock()
	h.catalog([]string{"personal"}, []string{"changed"})
	h.syncTaxonomy(false)
	tracked, err := h.app.Store.Setting(context.Background(), "taxonomy_message")
	if err != nil || tracked != 0 || len(h.api.live()) != 0 {
		t.Fatal("poll recreated manually deleted list")
	}
	h.send("/taxonomy")
	if len(h.api.live()) != 1 || h.api.live()[0].ResultID == id {
		t.Fatal("explicit command did not recreate deleted list")
	}
}

func TestSimultaneousTaxonomyRefreshesKeepOneListAndDraftUntouched(t *testing.T) {
	h := newHarness(t)
	d := h.ready("Saved body")
	var wg sync.WaitGroup
	errors := make(chan error, 8)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errors <- h.app.SyncTaxonomy(context.Background(), h.bot, true)
		}()
	}
	wg.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(h.api.live()) != 2 || h.api.count("sendMessage", false) != 2 {
		t.Fatal("concurrent refreshes created duplicate lists")
	}
	if got := h.draft(d.ID); got.Content != d.Content || !got.UpdatedAt.Equal(d.UpdatedAt) || got.CardID != d.CardID {
		t.Fatal("taxonomy poll changed draft data")
	}
}

func TestConcurrentDraftCardsRepliesAndNativeEdits(t *testing.T) {
	h := newHarness(t)
	a := h.ready("First body")
	rootA := h.messageID
	b := h.ready("Second body")
	rootB := h.messageID
	h.clickDraft(a.ID, "categories-0")
	h.clickDraft(a.ID, "category-1")
	h.clickDraft(a.ID, "tags-0")
	h.clickDraft(a.ID, "tag-0")
	h.clickDraft(a.ID, "back")
	h.clickDraft(a.ID, "preview")
	if h.active().ID != b.ID || h.draft(a.ID).Category != "personal" {
		t.Fatal("older card required resume or stole active selection")
	}
	h.api.mu.Lock()
	preview := h.api.messages[h.draft(a.ID).CardID].RichMarkdown
	h.api.mu.Unlock()
	if !strings.Contains(preview, "**Category:** personal") || !strings.Contains(preview, "**Tags:** go") {
		t.Fatal("preview missing chosen metadata")
	}
	h.replyTo(h.draft(a.ID).CardID, "First addition")
	h.replyTo(rootB, "Second addition")
	h.restart()
	if err := h.app.RestoreCard(context.Background(), h.bot); err != nil {
		t.Fatal(err)
	}
	if len(h.api.live()) != 2 {
		t.Fatal("restart added cards or discarded an open draft")
	}
	h.edit(rootA, "First revised\n\nSummary A\n\nFirst revised body\n\nCategory: personal\nTags: sqlite, go")
	h.edit(rootB, "Second revised\n\nSummary B\n\nSecond revised body")
	if h.draft(a.ID).Content != "First revised body\n\nFirst addition" || h.draft(b.ID).Content != "Second revised body\n\nSecond addition" || h.active().ID != b.ID {
		t.Fatal("edits/replies crossed draft boundaries")
	}
	h.replyTo(987654, "Must not append to second")
	if strings.Contains(h.draft(b.ID).Content, "Must not") {
		t.Fatal("unknown reply fell back to selected draft")
	}
	h.replyTo(rootA, "/publish")
	if !h.draft(a.ID).Exported || h.draft(b.ID).Number != 0 {
		t.Fatal("reply-scoped publication targeted wrong draft")
	}
	h.replyTo(rootB, "Still editable")
	if !strings.HasSuffix(h.draft(b.ID).Content, "Still editable") {
		t.Fatal("publishing one draft locked another")
	}
}

func TestConcurrentDraftFailureAndReplacementStayScoped(t *testing.T) {
	h := newHarness(t)
	a := h.ready("First")
	b := h.ready("Second")
	h.app.WriteFile = func(string, []byte) error { return fmt.Errorf("disk full") }
	h.clickDraft(a.ID, "publish")
	if h.draft(a.ID).Notice == "" || h.draft(b.ID).Notice != "" {
		t.Fatal("error annotated unrelated default draft")
	}
	h.clickDraft(b.ID, "options")
	h.clickDraft(b.ID, "replace")
	h.send("Replaced title\n\nSummary\n\nReplacement")
	if h.draft(b.ID).Content != "Replacement" || h.draft(a.ID).Content != "First" {
		t.Fatal("replacement crossed draft boundaries")
	}
}

func TestFooterUsesFreshCatalogAndDoesNotOverrideButtonChangesOnAppend(t *testing.T) {
	h := newHarness(t)
	h.send("/newpost")
	h.catalog([]string{"development", "personal"}, []string{"go", "fresh-tag"})
	h.send("Title\n\nSummary\n\nBody\n\nCategory: personal\nTags: fresh-tag, go")
	d := h.active()
	if d.Content != "Body" || d.Category != "personal" || strings.Join(d.Tags, ",") != "fresh-tag,go" {
		t.Fatal("fresh footer not parsed")
	}
	h.click("categories-0")
	h.click("category-0")
	h.click("tags-0")
	h.click("clear-tags")
	h.click("back")
	h.send("More body")
	h.restart()
	h.send("Another addition")
	if h.active().Category != "development" || len(h.active().Tags) != 0 || strings.Contains(h.active().Content, "Tags:") {
		t.Fatal("old footer overrode button edits or leaked into body")
	}
}

func TestTwentyTagsCanBeSelectedAcrossPagesAndPreviewed(t *testing.T) {
	h := newHarness(t)
	tags := make([]string, 20)
	for i := range tags {
		tags[i] = fmt.Sprintf("tag-%d", i)
	}
	h.catalog([]string{"development"}, tags)
	h.ready("Body")
	h.click("tags-0")
	for i := range tags {
		if i > 0 && i%choicesPerPage == 0 {
			h.click(fmt.Sprintf("tags-%d", i/choicesPerPage))
		}
		h.click(fmt.Sprintf("tag-%d", i))
	}
	h.click("back")
	h.click("preview")
	d := h.active()
	if len(d.Tags) != 20 || h.api.live()[0].RichMarkdown != previewMarkdown(d) {
		t.Fatal("pagination lost selections or preview truncated tags")
	}
}
