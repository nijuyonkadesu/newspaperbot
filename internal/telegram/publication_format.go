package telegram

import (
	"strings"

	"github.com/go-telegram/bot/models"
	"newspaperbot/internal/post"
)

// Publication-only text; source Markdown and owner previews never use this.
func publicationText(text, hashtags string) string {
	if hashtags = strings.Join(strings.Fields(hashtags), " "); hashtags == "" {
		return text
	}
	return strings.TrimRight(text, "\n") + "\n\n" + hashtags
}

func (a *App) publicationRich(d post.Draft) *models.InputRichMessage {
	rich := a.richMessage(d, false)
	rich.Markdown = publicationText(rich.Markdown, escapeRichText(a.PublishHashtags))
	return rich
}

func (a *App) publicationSummary(d post.Draft) (string, models.InputRichMessage) {
	text := d.Title + "\n\n" + d.Summary
	return publicationText(text, a.PublishHashtags), models.InputRichMessage{Markdown: publicationText("# "+text, escapeRichText(a.PublishHashtags))}
}
