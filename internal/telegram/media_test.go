package telegram

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/go-telegram/bot/models"
	"newspaperbot/internal/linkpreview"
	"newspaperbot/internal/media"
	"newspaperbot/internal/post"
	"newspaperbot/internal/store"
)

func (h *harness) sendMedia(m models.Message) int {
	h.t.Helper()
	h.messageID++
	m.ID, m.From, m.Chat = h.messageID, &models.User{ID: 42}, models.Chat{ID: 42, Type: models.ChatTypePrivate}
	h.app.Handle(context.Background(), h.bot, &models.Update{ID: int64(m.ID), Message: &m})
	return m.ID
}

func photoMessage(unique, caption string, reply int) models.Message {
	m := models.Message{Photo: []models.PhotoSize{{FileID: "small-" + unique, FileUniqueID: "small-" + unique, Width: 100, Height: 100}, {FileID: "large-" + unique, FileUniqueID: unique, Width: 1280, Height: 720}}, Caption: caption}
	if reply != 0 {
		m.ReplyToMessage = &models.Message{ID: reply}
	}
	return m
}

func TestIncompleteInitialPostKeepsPhotosThroughCorrectionAndRestart(t *testing.T) {
	for _, scenario := range []struct {
		name        string
		photoFirst  bool
		correctEdit bool
	}{
		{"photo first, edit text", true, true},
		{"photo first, new text", true, false},
		{"text first, edit text", false, true},
		{"text first, new text", false, false},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			h := newHarness(t)
			h.send("/newpost")
			cardID := h.active().CardID
			firstPhotoID := 0
			if scenario.photoFirst {
				firstPhotoID = h.sendMedia(photoMessage("first-pending", "First caption", cardID))
			}
			h.send("Incomplete title")
			textID := h.messageID
			if !scenario.photoFirst {
				firstPhotoID = h.sendMedia(photoMessage("first-pending", "First caption", cardID))
			}
			h.edit(textID, "Still incomplete title")
			secondPhotoID := h.sendMedia(photoMessage("second-pending", "Second caption", cardID))
			h.restart()
			if scenario.correctEdit {
				h.edit(textID, "Title\n\nSummary\n\nCorrected body")
			} else {
				h.send("Title\n\nSummary\n\nCorrected body")
			}
			d := h.active()
			if err := d.Validate(); err != nil {
				t.Fatal("corrected post is not publishable", err)
			}
			first := post.ImageURLDir + post.ImageAsset("first-pending", "jpg")
			second := post.ImageURLDir + post.ImageAsset("second-pending", "jpg")
			if len(d.ImageFiles()) != 2 || strings.Count(d.Content, "First caption") != 1 || strings.Count(d.Content, "Second caption") != 1 || strings.Index(d.Content, first) >= strings.Index(d.Content, second) {
				t.Fatal("correction lost, duplicated, or reordered photos/captions", d.Content)
			}
			for _, id := range []int{firstPhotoID, secondPhotoID} {
				linked, err := h.app.Store.FromMessage(context.Background(), id)
				if err != nil || linked.ID != d.ID {
					t.Fatal("photo lost its draft association", id, err)
				}
			}
			data, err := d.Markdown()
			if err != nil || !strings.Contains(string(data), first) || !strings.Contains(string(data), second) || !strings.Contains(string(data), ":::caption\nFirst caption\n:::") || !strings.Contains(string(data), ":::caption\nSecond caption\n:::") {
				t.Fatal("download/export lost photos or captions", string(data), err)
			}
			h.click("preview")
			live := h.api.live()
			if h.active().CardID != cardID || h.api.count("sendMessage", false) != 1 || len(live) != 1 || len(live[0].RichMedia) != 2 {
				t.Fatal("correction replaced the card or rich preview lost photos")
			}
		})
	}
}

func TestOwnPhotosReplyToTheirDraftWithoutDownloadingAndSurviveRestart(t *testing.T) {
	h := newHarness(t)
	first := h.ready("First body")
	h.click("preview")
	first = h.active()
	second := h.ready("Second body")
	m := photoMessage("own-photo", "Guide\nCategory: personal\nTags: sqlite", first.CardID)
	m.CaptionEntities = []models.MessageEntity{{Type: models.MessageEntityTypeTextLink, Offset: 0, Length: 5, URL: "https://site.test/guide"}}
	id := h.sendMedia(m)
	d := h.draft(first.ID)
	name := post.ImageAsset("own-photo", "jpg")
	if d.CardID != first.CardID || !d.Preview || h.active().ID != second.ID || d.Category != first.Category || len(d.Tags) != 0 || !strings.Contains(d.Content, "[Guide](https://site.test/guide)\nCategory: personal\nTags: sqlite") {
		t.Fatal("photo/caption changed routing, taxonomy, or preview state", d.Content)
	}
	if d.Images[name].FileID != "large-own-photo" || h.api.count("getFile", false) != 0 || h.api.count("sendMessage", false) != 2 {
		t.Fatal("drafting fetched media, chose a thumbnail, or added a new message")
	}
	before, _ := d.Markdown()
	if !strings.Contains(string(before), post.ImageURLDir+name) || strings.Contains(string(before), "large-own-photo") || strings.Contains(string(before), "tg://") {
		t.Fatal("portable Markdown lost the image or leaked Telegram references")
	}
	h.api.mu.Lock()
	preview := h.api.messages[first.CardID]
	h.api.mu.Unlock()
	if len(preview.RichMedia) != 1 || !strings.Contains(string(preview.RichMedia[0]), "large-own-photo") || !strings.Contains(preview.RichMarkdown, "tg://photo?id="+strings.TrimSuffix(name, ".jpg")+")") {
		t.Fatal("rich preview did not reuse the original Telegram photo", preview.RichMarkdown)
	}
	h.restart()
	m.ID, m.EditDate, m.From, m.Chat = id, 1, &models.User{ID: 42}, models.Chat{ID: 42, Type: models.ChatTypePrivate}
	m.Caption, m.CaptionEntities = "Edited caption", nil
	h.app.Handle(context.Background(), h.bot, &models.Update{ID: 999, EditedMessage: &m})
	d = h.draft(first.ID)
	if !strings.HasSuffix(d.Content, "Edited caption") || strings.Contains(d.Content, "[Guide]") || h.active().ID != second.ID {
		t.Fatal("caption edit after restart was lost or went to the selected draft")
	}
	h.replyTo(id, "/remove")
	if h.draft(first.ID).Content != "First body" || h.api.count("deleteMessage", false) != 2 {
		t.Fatal("/remove did not remove the photo/caption and clear the source/command")
	}
}

func TestVideoLinkRepliesAndEditsStayOutOfBodyAndKeepCaption(t *testing.T) {
	h := newHarness(t)
	h.app.Repository = &fakeRepository{}
	d := h.ready("Body")
	id := h.sendMedia(models.Message{Video: &models.Video{FileID: "video"}, Caption: "Caption\nCategory: personal\nTags: sqlite", ReplyToMessage: &models.Message{ID: d.CardID}})
	if len(h.active().MediaIssues()) != 1 || h.active().Validate() == nil {
		t.Fatal("unlinked video was silently publishable")
	}
	h.send("/publish")
	if jobs, _ := h.app.Store.PendingPublications(context.Background()); len(jobs) != 0 {
		t.Fatal("unlinked video queued a publication")
	}
	h.replyTo(id, "https://video.test/watch")
	linkID := h.messageID
	d = h.active()
	if d.Validate() != nil || !strings.Contains(d.Content, "[Watch video](https://video.test/watch)") || strings.Count(d.Content, "https://video.test/watch") != 1 || !strings.Contains(d.Content, "Category: personal\nTags: sqlite") {
		t.Fatal("video link was lost, duplicated as body text, or caption parsed as taxonomy", d.Content)
	}
	h.edit(linkID, "https://video.test/edited")
	if !strings.Contains(h.active().Content, "https://video.test/edited") || strings.Contains(h.active().Content, "/watch") {
		t.Fatal("video link reply edits were ignored")
	}
	h.replyTo(linkID, "/remove")
	if h.active().Validate() == nil || len(h.active().MediaIssues()) != 1 {
		t.Fatal("removing the link failed to restore video review")
	}
	h.replyTo(id, "/remove")
	if h.active().Content != "Body" {
		t.Fatal("video removal retained its caption or link")
	}
}

func TestAlbumStaysWithFirstDraftAndPublicForwardSourceIsAttributed(t *testing.T) {
	h := newHarness(t)
	first := h.ready("Body")
	m := photoMessage("first", "Album caption", first.CardID)
	m.MediaGroupID = "album"
	m.ForwardOrigin = &models.MessageOrigin{Type: models.MessageOriginTypeChannel, MessageOriginChannel: &models.MessageOriginChannel{Chat: models.Chat{Username: "public_source"}, MessageID: 41}}
	h.sendMedia(m)
	second := h.ready("Other body")
	m = photoMessage("second", "", 0)
	m.MediaGroupID = "album"
	m.ForwardOrigin = &models.MessageOrigin{Type: models.MessageOriginTypeChannel, MessageOriginChannel: &models.MessageOriginChannel{Chat: models.Chat{Username: "public_source"}, MessageID: 42}}
	h.sendMedia(m)
	d := h.draft(first.ID)
	if h.active().ID != second.ID || strings.Count(d.Content, "![](") != 2 || strings.Count(d.Content, "[Source]") != 1 || !strings.Contains(d.Content, "Album caption") {
		t.Fatal("album crossed drafts, lost caption, or repeated attribution", d.Content)
	}
}

func TestPublishWaitsForAlbumAndKeepsTheClickDate(t *testing.T) {
	h := newHarness(t)
	h.app.Repository = &fakeRepository{}
	h.ready("Body")
	m := photoMessage("first", "", 0)
	m.MediaGroupID = "album"
	h.sendMedia(m)
	h.send("/publish")
	d := h.active()
	requested := d.PublishRequestedAt
	if requested.IsZero() || d.Locked() {
		t.Fatal("album did not defer publication")
	}
	m = photoMessage("second", "", 0)
	m.MediaGroupID = "album"
	h.sendMedia(m)
	d = h.active()
	d.AlbumUpdatedAt = time.Now().Add(-time.Second)
	if err := h.app.Store.Save(context.Background(), &d); err != nil {
		t.Fatal(err)
	}
	h.app.flushMediaRequests(context.Background(), h.bot)
	jobs, err := h.app.Store.PendingPublications(context.Background())
	if err != nil || len(jobs) != 1 || len(jobs[0].Draft.ImageFiles()) != 2 || !jobs[0].Draft.PublishedAt.Equal(requested) {
		t.Fatal("album lost an item, duplicated Publish, or changed its click date", err)
	}
}

type imageFailureRepository struct{ *fakeRepository }

func (r *imageFailureRepository) Publish(ctx context.Context, job *store.Publication, checkpoint func() error) error {
	for _, id := range job.Draft.ImageFiles() {
		return &media.ImageError{FileID: id, Reason: "Image unavailable · retry Publish"}
	}
	return r.fakeRepository.Publish(ctx, job, checkpoint)
}

func TestPrecommitImageFailureReturnsEditableDraft(t *testing.T) {
	h := newHarness(t)
	h.app.Repository = &imageFailureRepository{&fakeRepository{}}
	d := h.ready("Body")
	id := h.sendMedia(photoMessage("bad", "Caption", d.CardID))
	h.send("/publish")
	h.finishGit()
	d = h.active()
	if d.Locked() || d.Slot == 0 || d.Number != 0 || d.Exported || len(d.MediaIssues()) != 1 || d.Notice != "" {
		t.Fatal("image failure trapped the draft or duplicated its review", d)
	}
	h.replyTo(id, "/remove")
	if h.active().Content != "Body" || h.active().Validate() != nil {
		t.Fatal("failed image could not be removed")
	}
	h.send("/publish")
	h.finishGit()
	if !h.active().Exported {
		t.Fatal("publication after removing the failed image did not complete")
	}
}

func TestLateAlbumItemsBecomePendingArticleEditsWithoutChangingSelection(t *testing.T) {
	h := newHarness(t)
	h.app.Repository = &fakeRepository{}
	first := h.ready("Body")
	m := photoMessage("first", "First caption", 0)
	m.MediaGroupID = "album"
	h.sendMedia(m)
	d := h.active()
	d.AlbumUpdatedAt = time.Now().Add(-time.Second)
	if err := h.app.Store.Save(context.Background(), &d); err != nil {
		t.Fatal(err)
	}
	h.send("/publish")
	date := h.active().PublishedAt.Format("2006-01-02")
	second := h.ready("Other body")
	m = photoMessage("late", "Late caption", 0)
	m.MediaGroupID = "album"
	id := h.sendMedia(m)
	d = h.draft(first.ID)
	if len(d.LateMedia) != 1 || strings.Contains(d.Content, "Late caption") {
		t.Fatal("late item overwrote the frozen publication")
	}
	m.ID, m.EditDate, m.From, m.Chat = id, 1, &models.User{ID: 42}, models.Chat{ID: 42, Type: models.ChatTypePrivate}
	m.Caption = "Edited late caption"
	h.app.Handle(context.Background(), h.bot, &models.Update{ID: 999, EditedMessage: &m})
	h.finishGit()
	d = h.draft(first.ID)
	if h.active().ID != second.ID || d.Revision == nil || d.Number != 269 || d.PublishedAt.Format("2006-01-02") != date || len(d.LateMedia) != 0 || len(d.ImageFiles()) != 2 || !strings.Contains(d.Content, "Edited late caption") {
		t.Fatal("late album lost a caption or changed article identity/selection", d)
	}
	h.restart()
	if h.draft(first.ID).Revision == nil || len(h.draft(first.ID).ImageFiles()) != 2 {
		t.Fatal("late album edits did not survive restart")
	}
}

func TestOwnImagesAndURLCardUseSameRichPreviewWithoutDroppingMedia(t *testing.T) {
	server, _, _ := previewWebsite(t, true)
	h := newHarness(t)
	h.app.Previews = linkpreview.New(server.Client())
	d := h.ready("[Website](" + server.URL + ")")
	h.sendMedia(photoMessage("own", "Caption", d.CardID))
	h.click("preview")
	h.waitPreviews()
	h.api.mu.Lock()
	preview := h.api.messages[d.CardID]
	h.api.mu.Unlock()
	if len(preview.RichMedia) != 2 || !strings.Contains(preview.RichMarkdown, "tg://photo?id="+strings.TrimSuffix(post.ImageAsset("own", "jpg"), ".jpg")+")") || !strings.Contains(preview.RichMarkdown, "newspaperbot_preview") {
		t.Fatal("URL enrichment dropped the owner's photo or its card", preview.RichMarkdown)
	}
}

func TestDiscardStopsDeferredAlbumSave(t *testing.T) {
	h := newHarness(t)
	h.app.Repository = &fakeRepository{entries: []post.Article{exampleArticle(t, 268, time.Now().UTC())}}
	h.send("/edit 268")
	m := photoMessage("pending", "", 0)
	m.MediaGroupID = "album"
	h.sendMedia(m)
	h.send("/save")
	if h.active().PublishRequestedAt.IsZero() {
		t.Fatal("save was not deferred for the album")
	}
	id := h.active().ID
	h.send("/cancel")
	h.app.flushMediaRequests(context.Background(), h.bot)
	if !h.draft(id).PublishRequestedAt.IsZero() {
		t.Fatal("discard left an automatic save pending")
	}
	if jobs, _ := h.app.Store.PendingPublications(context.Background()); len(jobs) != 0 {
		t.Fatal("discarded changes were published")
	}
}

func TestAttachmentsUseExplicitLinkReviewWithoutIgnoringCaption(t *testing.T) {
	h := newHarness(t)
	h.ready("Body")
	id := h.sendMedia(models.Message{Document: &models.Document{FileID: "archive", FileUniqueID: "archive", MimeType: "application/zip"}, Caption: "Attachment caption"})
	if !strings.Contains(h.active().Content, "Attachment caption") || h.active().Validate() == nil {
		t.Fatal("unsupported attachment/caption was silently ignored")
	}
	h.replyTo(id, "https://site.test/archive.zip")
	if h.active().Validate() != nil || !strings.Contains(h.active().Content, "[Open attachment](https://site.test/archive.zip)") {
		t.Fatal("attachment's public link was not accepted")
	}
}
