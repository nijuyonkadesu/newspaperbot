package post

import "strings"

// Reuse composition on a copy: only known photo captions gain markers. Existing
// unmarked paragraphs stay ordinary text, including in articles loaded from Git.
func (d Draft) exportContent() string {
	if len(d.Sources) != 0 && d.Invalid == "" {
		content := d.Content
		if err := d.rebuildWithCaptions(true); err != nil {
			return content // Downloads retain the last valid body during corrections.
		}
	}
	return d.Content
}

// ImageCaption marks known image captions for the portfolio Markdown renderer.
func ImageCaption(text string) string {
	width := 3
	for _, line := range strings.Split(text, "\n") {
		marker := strings.TrimSpace(line)
		if strings.Trim(marker, ":") == "" {
			width = max(width, len(marker)+1)
		}
	}
	marker := strings.Repeat(":", width)
	return marker + "caption\n" + text + "\n" + marker
}

// TelegramContent hides exported caption markers when reopening an article.
// Use the existing image scanner so code examples and escaped images stay literal.
func (d Draft) TelegramContent() string {
	var out strings.Builder
	cursor := 0
	rewriteImages(d.Content, func(_, _ string, start, end int) string {
		if start < cursor {
			return ""
		}
		lineStart := strings.LastIndexByte(d.Content[:start], '\n') + 1
		if start-lineStart > 3 || strings.Trim(d.Content[lineStart:start], " ") != "" {
			return ""
		}
		if lineStart > 0 {
			previous := d.Content[:lineStart-1]
			if strings.TrimSpace(previous[strings.LastIndexByte(previous, '\n')+1:]) != "" {
				return ""
			}
		}
		rest, _, ok := strings.Cut(d.Content[end:], "\n")
		if !ok || strings.TrimSpace(rest) != "" {
			return ""
		}
		opening := end + len(rest) + 1
		first, _, _ := strings.Cut(d.Content[opening:], "\n")
		if strings.TrimSpace(first) != "" {
			return ""
		}
		for opening < len(d.Content) {
			line, _, newline := strings.Cut(d.Content[opening:], "\n")
			if strings.TrimSpace(line) != "" {
				break
			}
			if !newline {
				return ""
			}
			opening += len(line) + 1
		}
		line, _, ok := strings.Cut(d.Content[opening:], "\n")
		marker, found := strings.CutSuffix(strings.TrimSpace(line), "caption")
		if !ok || !found || len(marker) < 3 || strings.Trim(marker, ":") != "" || strings.HasPrefix(line, "    ") || strings.HasPrefix(line, "\t") {
			return ""
		}
		caption := opening + len(line) + 1
		for closing := caption; closing < len(d.Content); {
			line, _, newline := strings.Cut(d.Content[closing:], "\n")
			if strings.TrimSpace(line) == marker && !strings.HasPrefix(line, "    ") && !strings.HasPrefix(line, "\t") && !insideFence(d.Content[caption:closing]) {
				out.WriteString(d.Content[cursor:opening])
				out.WriteString(d.Content[caption:max(caption, closing-1)])
				cursor = closing + len(line)
				return ""
			}
			if !newline {
				break
			}
			closing += len(line) + 1
		}
		return ""
	})
	if cursor == 0 {
		return d.Content
	}
	out.WriteString(d.Content[cursor:])
	return out.String()
}
