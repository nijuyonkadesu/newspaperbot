package linkpreview

import (
	"context"
	"encoding/json"
	"html"
	"net/url"
	"strings"
	"unicode/utf8"

	xhtml "golang.org/x/net/html"
)

func redditEndpoint(u *url.URL) string {
	switch strings.ToLower(u.Hostname()) {
	case "reddit.com", "www.reddit.com", "old.reddit.com", "np.reddit.com":
		if strings.Contains(u.Path, "/comments/") {
			return "https://www.reddit.com/oembed?url=" + url.QueryEscape(u.String())
		}
	}
	return ""
}

func redditContext(u *url.URL, author string) string {
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	description := "u/" + author
	if strings.HasPrefix(author, "[") {
		description = author
	}
	if len(parts) > 1 && parts[0] == "r" {
		description = "r/" + parts[1] + " · " + description
	}
	if strings.Contains(u.Path, "/comment/") {
		description = "Comment · " + description
	}
	return description
}

func twitterEndpoint(u *url.URL) string {
	switch strings.ToLower(u.Hostname()) {
	case "x.com", "www.x.com", "twitter.com", "www.twitter.com", "mobile.twitter.com":
		if strings.Contains(u.Path, "/status/") {
			return "https://publish.twitter.com/oembed?omit_script=true&url=" + url.QueryEscape(u.String())
		}
	}
	return ""
}

// oEmbed returns data, not executable widget HTML. Only the first paragraph's
// text is used when a provider such as X puts the post content in its widget.
func (c *Client) oembedMetadata(ctx context.Context, endpoint string) map[string]string {
	data, err := c.get(ctx, endpoint, 32<<10)
	var result struct {
		Title        string `json:"title"`
		Author       string `json:"author_name"`
		Description  string `json:"description"`
		ThumbnailURL string `json:"thumbnail_url"`
		HTML         string `json:"html"`
	}
	if err != nil || !utf8.Valid(data) || json.Unmarshal(data, &result) != nil {
		return nil
	}
	title, description := result.Title, result.Description
	if text := embedParagraph(result.HTML); text != "" {
		if title == "" {
			title = result.Author
		}
		if description == "" {
			description = text
		}
	}
	if description == "" {
		description = result.Author
	}
	return map[string]string{"og:title": html.UnescapeString(title), "og:description": html.UnescapeString(description), "og:image": result.ThumbnailURL}
}

func embedParagraph(widget string) string {
	z := xhtml.NewTokenizer(strings.NewReader(widget))
	inParagraph, skip := false, false
	var text strings.Builder
	for {
		switch z.Next() {
		case xhtml.ErrorToken:
			return ""
		case xhtml.StartTagToken:
			switch z.Token().Data {
			case "p":
				inParagraph = true
			case "script", "style":
				skip = true
			case "br":
				text.WriteByte(' ')
			}
		case xhtml.EndTagToken:
			switch z.Token().Data {
			case "p":
				if inParagraph {
					return strings.TrimSpace(text.String())
				}
			case "script", "style":
				skip = false
			}
		case xhtml.TextToken:
			if inParagraph && !skip {
				text.Write(z.Text())
			}
		}
	}
}
