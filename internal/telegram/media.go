package telegram

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"newspaperbot/internal/media"
	"newspaperbot/internal/post"
)

// DownloadMedia retrieves bytes at publication time, not while drafting.
// A local Bot API can return an absolute path; public API paths use HTTP.
func DownloadMedia(ctx context.Context, b *bot.Bot, id string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	f, err := b.GetFile(ctx, &bot.GetFileParams{FileID: id})
	if err != nil || f == nil || f.FilePath == "" {
		return nil, errors.New("Image unavailable from Bot API · retry Publish or resend it")
	}
	if f.FileSize > media.MaxImageBytes {
		return nil, errors.New("Image exceeds 5 MiB · resend a smaller image")
	}
	var reader io.ReadCloser
	if filepath.IsAbs(f.FilePath) {
		info, statErr := os.Stat(f.FilePath)
		if statErr != nil || !info.Mode().IsRegular() {
			return nil, errors.New("Bot API media path is not readable by this service")
		}
		if info.Size() > media.MaxImageBytes {
			return nil, errors.New("Image exceeds 5 MiB · resend a smaller image")
		}
		reader, err = os.Open(f.FilePath)
		if err != nil {
			return nil, errors.New("Bot API media path is not readable by this service")
		}
	} else {
		request, requestErr := http.NewRequestWithContext(ctx, http.MethodGet, b.FileDownloadLink(f), nil)
		if requestErr != nil {
			return nil, errors.New("Bot API returned an invalid file path")
		}
		client := &http.Client{Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		response, fetchErr := client.Do(request)
		if fetchErr != nil {
			return nil, errors.New("Image download failed · retry Publish")
		}
		reader = response.Body
		if response.StatusCode != http.StatusOK {
			reader.Close()
			return nil, errors.New("Image download failed · retry Publish")
		}
	}
	defer reader.Close()
	data, err := io.ReadAll(io.LimitReader(reader, media.MaxImageBytes+1))
	if err != nil {
		return nil, errors.New("Image download incomplete · retry Publish")
	}
	if len(data) > media.MaxImageBytes {
		return nil, errors.New("Image exceeds 5 MiB · resend a smaller image")
	}
	return data, nil
}

func importMedia(m *models.Message, updateID int64) (post.Source, string, bool) {
	meta := &post.Media{GroupID: m.MediaGroupID}
	photoID, unique, ext := "", "", "jpg"
	switch {
	case len(m.Photo) != 0:
		photo := slices.MaxFunc(m.Photo, func(a, b models.PhotoSize) int { return a.Width*a.Height - b.Width*b.Height })
		meta.Kind, meta.FileID, unique, photoID = "photo", photo.FileID, photo.FileUniqueID, photo.FileID
	case m.Document != nil && slices.Contains([]string{"image/jpeg", "image/png", "image/webp"}, m.Document.MimeType):
		meta.Kind, meta.FileID, unique = "image", m.Document.FileID, m.Document.FileUniqueID
		ext = strings.TrimPrefix(m.Document.MimeType, "image/")
		if ext == "jpeg" {
			ext = "jpg"
		}
		if m.Document.Thumbnail != nil {
			photoID = m.Document.Thumbnail.FileID
		}
	case m.Video != nil || m.Animation != nil:
		meta.Kind = "video"
	case m.Document != nil || m.Audio != nil || m.Voice != nil || m.VideoNote != nil || m.Sticker != nil:
		meta.Kind = "file"
	default:
		return post.Source{}, "", false
	}
	if unique != "" {
		meta.Asset = post.ImageAsset(unique, ext)
	}
	if origin := m.ForwardOrigin; origin != nil && origin.Type == models.MessageOriginTypeChannel && origin.MessageOriginChannel != nil {
		channel := origin.MessageOriginChannel
		if channel.Chat.Username != "" && channel.MessageID > 0 {
			meta.Origin = fmt.Sprintf("https://t.me/%s/%d", channel.Chat.Username, channel.MessageID)
		}
	}
	caption, incomplete := entityMarkdown(m.Caption, m.CaptionEntities)
	issue := ""
	if incomplete {
		issue = "caption formatting kept as text"
	}
	// PhotoID is presentation metadata; the original FileID remains the source
	// for the repository file. Documents without thumbnails stay downloadable.
	metaSource := post.Source{MessageID: m.ID, UpdateID: updateID, EditDate: m.EditDate, Text: caption, Media: meta}
	meta.PhotoID = photoID
	return metaSource, issue, true
}

func (a *App) mediaMessage(ctx context.Context, b *bot.Bot, m *models.Message, updateID int64) (bool, error) {
	source, issue, supported := importMedia(m, updateID)
	if !supported {
		return false, nil
	}
	var d post.Draft
	var err error
	if m.MediaGroupID != "" {
		d, err = a.Store.FromAlbum(ctx, m.MediaGroupID)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return true, err
		}
	}
	if d.ID == 0 {
		d, err = a.target(ctx, b, m)
	}
	if err != nil || d.ID == 0 {
		return true, err
	}
	for _, old := range append(slices.Clone(d.Sources), d.LateMedia...) {
		if old.MessageID == m.ID {
			return true, nil
		}
	}
	if d.Locked() {
		if m.MediaGroupID == "" {
			return true, a.notice(ctx, b, &d, "Saving in progress · add media after /edit")
		}
		d.LateMedia = append(d.LateMedia, source)
		setMessageIssue(&d, m.ID, updateID, m.EditDate, issue)
		if err := a.Store.Save(ctx, &d); err != nil {
			return true, err
		}
		if d.Exported && (d.ChannelID == 0 || d.Delivery == "sent") && d.Revision == nil {
			return true, a.applyLateMedia(ctx, b, &d)
		}
		return true, a.render(ctx, b, &d)
	}
	if d.View == "replace" {
		return true, a.notice(ctx, b, &d, "Send replacement text first · then add media")
	}
	if err := a.appendMedia(&d, source); err != nil {
		return true, err
	}
	setMessageIssue(&d, m.ID, updateID, m.EditDate, issue)
	if err := a.Store.Save(ctx, &d); err != nil {
		return true, err
	}
	return true, a.render(ctx, b, &d)
}

func (a *App) appendMedia(d *post.Draft, source post.Source) error {
	if source.Media.GroupID != "" {
		d.AlbumUpdatedAt = time.Now().UTC()
	}
	d.LastMessageID = max(d.LastMessageID, source.MessageID)
	return d.Append(source)
}

func mediaURL(m *models.Message) string {
	if value := post.PublicMediaURL(m.Text); value != "" {
		return value
	}
	for _, e := range m.Entities {
		if e.Type == models.MessageEntityTypeTextLink {
			start, end, ok := utf16Range(m.Text, e.Offset, e.Length)
			if ok && start == 0 && end == len(m.Text) {
				return post.PublicMediaURL(e.URL)
			}
		}
	}
	return ""
}

func (a *App) richMessage(d post.Draft, footer bool) *models.InputRichMessage {
	rich := &models.InputRichMessage{Markdown: d.RichMarkdown()}
	if footer {
		rich.Markdown = previewMarkdown(d)
	}
	seen := map[string]bool{}
	rich.Markdown = post.RewriteImages(rich.Markdown, func(name, alt string) string {
		ref := d.Images[name]
		if ref.FileID == "" {
			return ""
		}
		id := strings.SplitN(name, ".", 2)[0] // Full 64-character hash fits Telegram's media ID limit.
		var input models.InputMedia
		link := "tg://photo?id=" + id
		if ref.PhotoID != "" {
			input = &models.InputMediaPhoto{Media: ref.PhotoID}
		} else {
			input = &models.InputMediaDocument{Media: ref.FileID}
			link = "tg://document?id=" + id
		}
		if !seen[id] {
			rich.Media = append(rich.Media, models.InputRichMessageMedia{ID: id, Media: input})
			seen[id] = true
		}
		if ref.PhotoID == "" {
			return "[Image file](" + link + ")"
		}
		return "![" + alt + "](" + link + ")"
	})
	return rich
}

func (a *App) applyLateMedia(ctx context.Context, b *bot.Bot, d *post.Draft) error {
	if len(d.LateMedia) == 0 || a.Repository == nil {
		return nil
	}
	articles, err := a.Repository.Articles(ctx)
	if err != nil {
		return err
	}
	for _, article := range articles {
		if article.Number != d.Number {
			continue
		}
		pending := slices.Clone(d.LateMedia)
		active, err := a.Store.Setting(ctx, "active")
		if err != nil {
			return err
		}
		*d, err = a.Store.StartRevision(ctx, article)
		if err != nil {
			return err
		}
		if active != d.ID {
			if err := a.Store.SetSetting(ctx, "active", active); err != nil {
				return err
			}
		}
		d.LateMedia = nil
		for _, source := range pending {
			if err := a.appendMedia(d, source); err != nil {
				return err
			}
		}
		d.Notice = "Late album items · save these changes"
		if err := a.Store.Save(ctx, d); err != nil {
			return err
		}
		return a.render(ctx, b, d)
	}
	return errors.New("published article unavailable for late album items")
}
