package linkpreview

import (
	"net/url"
	"strings"
	"unicode"
)

// firstURL leaves fenced/indented code and inline code alone. Link destinations
// and plain URLs are both eligible; balanced parentheses remain part of a URL.
func firstURL(markdown string) string {
	fence := ""
	for _, line := range strings.Split(markdown, "\n") {
		if strings.HasPrefix(line, "    ") || strings.HasPrefix(line, "\t") {
			continue
		}
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			end := strings.IndexFunc(trimmed, func(r rune) bool { return r != rune(trimmed[0]) })
			if end < 0 {
				end = len(trimmed)
			}
			marker := trimmed[:end]
			if fence == "" {
				fence = marker
			} else if strings.HasPrefix(marker, fence) && strings.TrimSpace(trimmed[end:]) == "" {
				fence = ""
			}
			continue
		}
		if fence != "" {
			continue
		}
		for i := 0; i < len(line); i++ {
			if line[i] == '`' {
				end := i + 1
				for end < len(line) && line[end] == '`' {
					end++
				}
				delimiter := line[i:end]
				close := strings.Index(line[end:], delimiter)
				if close < 0 {
					break
				}
				i = end + close + len(delimiter) - 1
				continue
			}
			if !strings.HasPrefix(line[i:], "https://") && !strings.HasPrefix(line[i:], "http://") {
				continue
			}
			text := line[i:]
			depth := [3]int{}
			for end, r := range text {
				stop := unicode.IsSpace(r) || strings.ContainsRune("<>\"'`", r)
				for j, pair := range [][2]rune{{'(', ')'}, {'[', ']'}, {'{', '}'}} {
					if r == pair[0] {
						depth[j]++
					} else if r == pair[1] {
						if depth[j] == 0 {
							stop = true
						}
						depth[j]--
					}
				}
				if stop {
					text = text[:end]
					break
				}
			}
			text = strings.TrimRight(text, ".,;!?")
			if u, err := url.Parse(text); err == nil && webURL(u) {
				return u.String()
			}
		}
	}
	return ""
}
