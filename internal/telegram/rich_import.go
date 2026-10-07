package telegram

import (
	"encoding/json"
	"fmt"
	"maps"
	"strings"
	"unicode/utf16"

	"github.com/go-telegram/bot/models"
	"newspaperbot/internal/post"
)

type richImporter struct {
	incomplete bool
	images     map[string]post.ImageRef
}

func importRichMessage(message *models.RichMessage) (string, string, map[string]post.ImageRef) {
	i := richImporter{images: map[string]post.ImageRef{}}
	text := i.blocks(message.Blocks, 0)
	issue := ""
	if i.incomplete || text == "" {
		issue = "rich content needs review"
	}
	return text, issue, i.images
}

func rememberRichImages(d *post.Draft, images map[string]post.ImageRef) {
	if len(images) == 0 {
		return
	}
	if d.Images == nil {
		d.Images = map[string]post.ImageRef{}
	}
	maps.Copy(d.Images, images)
}

func (i *richImporter) blocks(blocks []models.RichBlock, depth int) string {
	if depth > 16 {
		i.incomplete = true
		return ""
	}
	var parts []string
	for _, block := range blocks {
		text := ""
		switch {
		case block.Type == models.RichBlockTypeParagraph && block.RichBlockParagraph != nil:
			text = i.text(block.RichBlockParagraph.Text, false, 0)
		case block.Type == models.RichBlockTypeSectionHeading && block.RichBlockSectionHeading != nil:
			b := block.RichBlockSectionHeading
			text = strings.Repeat("#", max(1, min(6, b.Size))) + " " + i.text(b.Text, false, 0)
		case block.Type == models.RichBlockTypePreformatted && block.RichBlockPreformatted != nil:
			b := block.RichBlockPreformatted
			body := i.text(b.Text, true, 0)
			text, _ = entityMarkdown(body, []models.MessageEntity{{Type: models.MessageEntityTypePre, Length: len(utf16.Encode([]rune(body))), Language: b.Language}})
			if !validLanguage(b.Language) {
				i.incomplete = true
			}
		case block.Type == models.RichBlockTypeFooter && block.RichBlockFooter != nil:
			text = i.text(block.RichBlockFooter.Text, false, 0)
		case block.Type == models.RichBlockTypeDivider:
			text = "---"
		case block.Type == models.RichBlockTypeList && block.RichBlockList != nil:
			var items []string
			for _, item := range block.RichBlockList.Items {
				prefix := "- "
				if item.Value > 0 {
					prefix = fmt.Sprintf("%d. ", item.Value)
				}
				if item.HasCheckbox {
					prefix = "- [ ] "
					if item.IsChecked {
						prefix = "- [x] "
					}
				}
				if item.Type != "" && item.Type != "1" {
					i.incomplete = true
				}
				body := i.blocks(item.Blocks, depth+1)
				indent := len(prefix)
				if item.HasCheckbox {
					indent = 2
				}
				items = append(items, prefix+strings.ReplaceAll(body, "\n", "\n"+strings.Repeat(" ", indent)))
			}
			text = strings.Join(items, "\n\n")
		case block.Type == models.RichBlockTypeBlockQuotation && block.RichBlockBlockQuotation != nil:
			b := block.RichBlockBlockQuotation
			text = i.blocks(b.Blocks, depth+1)
			if b.Credit != nil {
				text += "\n\n" + i.text(*b.Credit, false, 0)
			}
			text = "> " + strings.ReplaceAll(text, "\n", "\n> ")
		case block.Type == models.RichBlockTypePhoto && block.RichBlockPhoto != nil:
			b := block.RichBlockPhoto
			source, _, ok := importMedia(&models.Message{Photo: b.Photo}, 0)
			if !ok || source.Media.FileID == "" || !post.ValidImageAsset(source.Media.Asset) {
				i.incomplete = true
				break
			}
			m := source.Media
			i.images[m.Asset] = post.ImageRef{FileID: m.FileID, PhotoID: m.PhotoID}
			text = "![](" + post.ImageURLDir + m.Asset + ")"
			if b.Caption != nil {
				if caption := i.text(b.Caption.Text, false, 0); caption != "" {
					text += "\n\n" + post.ImageCaption(caption)
				}
				if b.Caption.Credit != nil {
					text += "\n\n" + i.text(*b.Caption.Credit, false, 0)
				}
			}
		case block.Type == models.RichBlockTypeCollage && block.RichBlockCollage != nil:
			text = i.blocks(block.RichBlockCollage.Blocks, depth+1)
			if block.RichBlockCollage.Caption != nil {
				text += "\n\n" + i.text(block.RichBlockCollage.Caption.Text, false, 0)
				if credit := block.RichBlockCollage.Caption.Credit; credit != nil {
					text += "\n\n" + i.text(*credit, false, 0)
				}
			}
		case block.Type == models.RichBlockTypeTable && block.RichBlockTable != nil:
			rows := block.RichBlockTable.Cells
			if len(rows) == 0 || len(rows[0]) == 0 {
				i.incomplete = true
				break
			}
			columns := len(rows[0])
			for _, row := range rows {
				columns = max(columns, len(row))
			}
			var lines []string
			for rowIndex, row := range rows {
				cells := make([]string, columns)
				for col, cell := range row {
					if cell.Text != nil {
						cells[col] = i.text(*cell.Text, false, 0)
						if strings.Contains(cells[col], "\n") {
							i.incomplete = true
							cells[col] = strings.ReplaceAll(cells[col], "\n", " ")
						}
					}
					if cell.Colspan > 1 || cell.Rowspan > 1 {
						i.incomplete = true
					}
				}
				lines = append(lines, "| "+strings.Join(cells, " | ")+" |")
				if rowIndex == 0 {
					for col := range cells {
						cells[col] = ":---"
						if col >= len(row) {
							continue
						}
						cell := row[col]
						if cell.Align == "right" || cell.Align == "center" {
							cells[col] = "---:"
							if cell.Align == "center" {
								cells[col] = ":---:"
							}
						}
					}
					lines = append(lines, "| "+strings.Join(cells, " | ")+" |")
				}
			}
			text = strings.Join(lines, "\n")
			if block.RichBlockTable.Caption != nil {
				text += "\n\n" + i.text(*block.RichBlockTable.Caption, false, 0)
			}
		default:
			i.incomplete = true
			// Preserve accessible text/captions and nested content even when the
			// surrounding layout or media type has no supported Markdown mapping.
			data, err := json.Marshal(block)
			var fallback struct {
				Text    models.RichText    `json:"text"`
				Summary models.RichText    `json:"summary"`
				Blocks  []models.RichBlock `json:"blocks"`
				Caption json.RawMessage    `json:"caption"`
			}
			if err == nil && json.Unmarshal(data, &fallback) == nil {
				text = i.text(fallback.Summary, false, 0) + "\n\n" + i.text(fallback.Text, false, 0) + "\n\n" + i.blocks(fallback.Blocks, depth+1)
				var caption models.RichBlockCaption
				if len(fallback.Caption) != 0 && json.Unmarshal(fallback.Caption, &caption) == nil {
					text += "\n\n" + i.text(caption.Text, false, 0)
					if caption.Credit != nil {
						text += "\n\n" + i.text(*caption.Credit, false, 0)
					}
				}
				text = strings.TrimSpace(text)
			}
		}
		if text != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "\n\n")
}

func (i *richImporter) text(text models.RichText, raw bool, depth int) string {
	if depth > 16 {
		i.incomplete = true
		return ""
	}
	if text.Array != nil {
		var out strings.Builder
		for _, child := range text.Array {
			out.WriteString(i.text(child, raw, depth+1))
		}
		return out.String()
	}
	if text.Type == "" {
		if raw {
			return text.PlainText
		}
		return escapeImportedText(text.PlainText)
	}
	// The SDK owns the tagged union. Read its common text field rather than
	// maintaining another switch across every variant's pointer field.
	data, err := json.Marshal(text)
	var value struct {
		Text            models.RichText `json:"text"`
		URL             string          `json:"url"`
		EmailAddress    string          `json:"email_address"`
		PhoneNumber     string          `json:"phone_number"`
		AlternativeText string          `json:"alternative_text"`
	}
	if err != nil || json.Unmarshal(data, &value) != nil {
		i.incomplete = true
		return ""
	}
	if text.Type == models.RichTextTypeCustomEmoji {
		if raw {
			return value.AlternativeText
		}
		return escapeImportedText(value.AlternativeText)
	}
	if raw {
		return i.text(value.Text, true, depth+1)
	}
	body := i.text(value.Text, false, depth+1)
	switch text.Type {
	case models.RichTextTypeBold:
		return "**" + body + "**"
	case models.RichTextTypeItalic:
		return "*" + body + "*"
	case models.RichTextTypeStrikethrough:
		return "~~" + body + "~~"
	case models.RichTextTypeCode:
		body = i.text(value.Text, true, depth+1)
		code, _ := entityMarkdown(body, []models.MessageEntity{{Type: models.MessageEntityTypeCode, Length: len(utf16.Encode([]rune(body)))}})
		return code
	case models.RichTextTypeURL:
		if value.URL != "" && !strings.ContainsAny(value.URL, "\r\n") {
			return "[" + body + "](" + escapeLinkURL(value.URL) + ")"
		}
	case models.RichTextTypeEmailAddress:
		return "[" + body + "](mailto:" + escapeLinkURL(value.EmailAddress) + ")"
	case models.RichTextTypePhoneNumber:
		return "[" + body + "](tel:" + escapeLinkURL(value.PhoneNumber) + ")"
	case models.RichTextTypeMention, models.RichTextTypeHashtag, models.RichTextTypeCashtag, models.RichTextTypeBotCommand, models.RichTextTypeBankCardNumber, models.RichTextTypeDateTime, models.RichTextTypeTextMention:
		return body
	}
	i.incomplete = true
	return body
}

func escapeImportedText(text string) string {
	text = strings.NewReplacer("\\", "\\\\", "*", "\\*", "_", "\\_", "[", "\\[", "]", "\\]", "`", "\\`", "~", "\\~", "|", "\\|", "<", "&lt;", ">", "&gt;", "&", "&amp;").Replace(text)
	lines := strings.Split(text, "\n")
	for index, line := range lines {
		trimmed := strings.TrimLeft(line, " ")
		indent := len(line) - len(trimmed)
		if indent > 3 || trimmed == "" {
			continue
		}
		if strings.ContainsAny(trimmed[:1], "#-+") {
			lines[index] = line[:indent] + "\\" + line[indent:]
		} else if digits, suffix, ok := strings.Cut(trimmed, "."); ok && digits != "" && strings.Trim(digits, "0123456789") == "" && strings.HasPrefix(suffix, " ") {
			lines[index] = line[:indent+len(digits)] + "\\" + line[indent+len(digits):]
		}
	}
	return strings.Join(lines, "\n")
}
