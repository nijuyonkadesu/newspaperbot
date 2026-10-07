package telegram

import (
	"sort"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/go-telegram/bot/models"
)

type entityMark struct {
	start, end  int
	open, close string
	link        bool
	order       int
}

func importText(m *models.Message) (text, issue string) {
	if m.RichMessage != nil {
		return "", "rich content skipped"
	}
	if m.Text == "" { // Captions are handled by the media importer.
		return "", ""
	}
	text, incomplete := entityMarkdown(m.Text, m.Entities)
	if incomplete {
		issue = "formatting kept as text"
	}
	return text, issue
}

// entityMarkdown retains the original text and inserts Markdown only for
// formatting Telegram stores outside it. In particular, text_link URLs would
// otherwise be lost entirely.
func entityMarkdown(text string, entities []models.MessageEntity) (string, bool) {
	marks := make([]entityMark, 0, len(entities))
	incomplete := false
	for _, entity := range entities {
		start, end, ok := utf16Range(text, entity.Offset, entity.Length)
		if !ok {
			incomplete = true
			continue
		}
		mark := entityMark{start: start, end: end}
		switch entity.Type {
		case models.MessageEntityTypeBold:
			mark.open, mark.close = "**", "**"
		case models.MessageEntityTypeItalic:
			mark.open, mark.close = "*", "*"
		case models.MessageEntityTypeStrikethrough:
			mark.open, mark.close = "~~", "~~"
		case models.MessageEntityTypeCode:
			mark.open = strings.Repeat("`", longestRun(text[start:end], '`')+1)
			mark.close = mark.open
		case models.MessageEntityTypePre:
			language := entity.Language
			if !validLanguage(language) {
				language, incomplete = "", true
			}
			mark.open = strings.Repeat("`", max(3, longestRun(text[start:end], '`')+1)) + language + "\n"
			mark.close = "\n" + strings.TrimSuffix(mark.open, language+"\n")
		case models.MessageEntityTypeTextLink:
			if entity.URL == "" {
				incomplete = true
				continue
			}
			mark.open, mark.close, mark.link = "[", "]("+escapeLinkURL(entity.URL)+")", true
		case models.MessageEntityTypeMention, models.MessageEntityTypeHashtag,
			models.MessageEntityTypeCashtag, models.MessageEntityTypeBotCommand,
			models.MessageEntityTypeURL, models.MessageEntityTypeEmail,
			models.MessageEntityTypePhoneNumber, models.MessageEntityTypeTextMention,
			models.MessageEntityTypeCustomEmoji, models.MessageEntityTypeDateTime:
			continue // Their useful representation is already present in text.
		default:
			incomplete = true
			continue
		}
		marks = append(marks, mark)
	}

	// Telegram entities are normally nested or disjoint. If malformed entities
	// cross, retain the higher-value text link and skip only the conflicting mark.
	sort.SliceStable(marks, func(i, j int) bool { return marks[i].link && !marks[j].link })
	accepted := marks[:0]
	for _, mark := range marks {
		conflict := false
		for _, previous := range accepted {
			if crossing(mark, previous) || mark.link && previous.link && overlaps(mark, previous) {
				conflict = true
				break
			}
		}
		if conflict {
			incomplete = true
			continue
		}
		accepted = append(accepted, mark)
	}
	marks = accepted
	sort.SliceStable(marks, func(i, j int) bool {
		if marks[i].start != marks[j].start {
			return marks[i].start < marks[j].start
		}
		if marks[i].end != marks[j].end {
			return marks[i].end > marks[j].end
		}
		return marks[i].link && !marks[j].link
	})

	opens, closes := map[int][]int{}, map[int][]int{}
	points := []int{0, len(text)}
	for i := range marks {
		marks[i].order = i
		opens[marks[i].start] = append(opens[marks[i].start], i)
		closes[marks[i].end] = append(closes[marks[i].end], i)
		points = append(points, marks[i].start, marks[i].end)
	}
	sort.Ints(points)
	points = compact(points)

	var out strings.Builder
	previous, links := 0, 0
	for _, point := range points {
		segment := text[previous:point]
		if links > 0 {
			segment = strings.NewReplacer(`\`, `\\`, `[`, `\[`, `]`, `\]`).Replace(segment)
		}
		out.WriteString(segment)
		sort.Slice(closes[point], func(i, j int) bool { return marks[closes[point][i]].order > marks[closes[point][j]].order })
		for _, index := range closes[point] {
			out.WriteString(marks[index].close)
			if marks[index].link {
				links--
			}
		}
		for _, index := range opens[point] {
			out.WriteString(marks[index].open)
			if marks[index].link {
				links++
			}
		}
		previous = point
	}
	return out.String(), incomplete
}

func utf16Range(text string, offset, length int) (int, int, bool) {
	if offset < 0 || length <= 0 || offset+length < offset {
		return 0, 0, false
	}
	targetStart, targetEnd := offset, offset+length
	units, start, end := 0, -1, -1
	for byteOffset, r := range text {
		if units == targetStart {
			start = byteOffset
		}
		if units == targetEnd {
			end = byteOffset
		}
		units += utf16.RuneLen(r)
	}
	if units == targetStart {
		start = len(text)
	}
	if units == targetEnd {
		end = len(text)
	}
	return start, end, start >= 0 && end > start && utf8.ValidString(text[start:end])
}

func validLanguage(language string) bool {
	for _, r := range language {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("_+-.#", r)) {
			return false
		}
	}
	return true
}

func longestRun(text string, target byte) int {
	longest, current := 0, 0
	for i := range len(text) {
		if text[i] == target {
			current++
			longest = max(longest, current)
		} else {
			current = 0
		}
	}
	return longest
}

func escapeLinkURL(url string) string {
	return strings.NewReplacer(`\`, `\\`, `)`, `\)`).Replace(url)
}

func overlaps(a, b entityMark) bool { return a.start < b.end && b.start < a.end }

func crossing(a, b entityMark) bool {
	return overlaps(a, b) && !(a.start <= b.start && a.end >= b.end) && !(b.start <= a.start && b.end >= a.end)
}

func compact(values []int) []int {
	result := values[:0]
	for _, value := range values {
		if len(result) == 0 || result[len(result)-1] != value {
			result = append(result, value)
		}
	}
	return result
}
