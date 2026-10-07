package post

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"
)

const ImageAssetDir = "src/assets/images/posts/"
const ImageURLDir = "/assets/images/posts/"
const MaxImages = 20

// Media stores Telegram references, never image bytes or bot download URLs.
type Media struct {
	Kind, FileID, PhotoID, GroupID, Asset, Origin, Error string
}

type ImageRef struct{ FileID, PhotoID string }

func ImageAsset(uniqueID, extension string) string {
	return fmt.Sprintf("%x.%s", sha256.Sum256([]byte(uniqueID)), extension)
}

func ValidImageAsset(name string) bool {
	stem, ext, ok := strings.Cut(name, ".")
	return ok && len(stem) == 64 && strings.Trim(stem, "0123456789abcdef") == "" && slices.Contains([]string{"jpg", "png", "webp"}, ext)
}

func PublicMediaURL(text string) string {
	text = strings.TrimSpace(text)
	u, err := url.Parse(text)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Hostname() == "" || u.User != nil || strings.ContainsAny(text, "\r\n\t ") {
		return ""
	}
	if strings.EqualFold(u.Hostname(), "t.me") && strings.HasPrefix(u.Path, "/c/") {
		return ""
	}
	return u.String()
}

func (d Draft) mediaLinks() map[int]string {
	links := map[int]string{}
	for _, source := range d.Sources {
		if source.Media != nil {
			links[source.MessageID] = source.Media.Origin
		}
		if source.MediaFor != 0 {
			links[source.MediaFor] = PublicMediaURL(source.Text)
		}
	}
	return links
}

func (d Draft) MediaIssues() []MessageIssue {
	issues := slices.Clone(d.MessageIssues)
	links := d.mediaLinks()
	for _, source := range d.Sources {
		if source.Media != nil && source.Media.Error != "" {
			issues = append(issues, MessageIssue{MessageID: source.MessageID, Reason: source.Media.Error})
		}
		if source.Media != nil && source.Media.linkOnly() && links[source.MessageID] == "" {
			issues = append(issues, MessageIssue{MessageID: source.MessageID, Reason: source.Media.linkLabel() + " needs a public URL · reply with its link"})
		}
	}
	return issues
}

func (d *Draft) rememberImage(m *Media) {
	if m == nil || m.Kind == "video" || !ValidImageAsset(m.Asset) {
		return
	}
	if d.Images == nil {
		d.Images = map[string]ImageRef{}
	}
	d.Images[m.Asset] = ImageRef{FileID: m.FileID, PhotoID: m.PhotoID}
}

func (m Media) linkOnly() bool { return m.Kind == "video" || m.Kind == "file" }
func (m Media) linkLabel() string {
	if m.Kind == "video" {
		return "Video"
	}
	return "Attachment"
}

func (d Draft) ValidateMedia() error {
	if len(d.ImageFiles()) > MaxImages {
		return fmt.Errorf("keep images within %d per post", MaxImages)
	}
	for _, source := range d.Sources {
		if source.Media != nil && !source.Media.linkOnly() && (!ValidImageAsset(source.Media.Asset) || source.Media.FileID == "") {
			return errors.New("Image unavailable · resend it or remove its message")
		}
	}
	links := d.mediaLinks()
	for _, source := range d.Sources {
		if source.Media != nil && source.Media.linkOnly() && links[source.MessageID] == "" {
			return errors.New(source.Media.linkLabel() + " needs a public URL · reply with its link or /remove")
		}
	}
	return nil
}

// ImageFiles includes only image references present in the current article.
// Removed images and code examples never become publication attachments.
func (d Draft) ImageFiles() map[string]string {
	files := map[string]string{}
	RewriteImages(d.Content, func(name, alt string) string {
		if id := d.Images[name].FileID; id != "" {
			files[name] = id
		}
		return ""
	})
	return files
}

// RewriteImages only rewrites bot-owned Markdown image destinations, leaving
// fenced and inline code, ordinary links, and external images untouched.
func RewriteImages(markdown string, replace func(string, string) string) string {
	var out strings.Builder
	var fence byte
	var width int
	offset, inlineEnd := 0, 0
	for _, line := range strings.SplitAfter(markdown, "\n") {
		startOffset := offset
		offset += len(line)
		trimmed := strings.TrimLeft(line, " ")
		if startOffset >= inlineEnd && len(line)-len(trimmed) < 4 && len(trimmed) >= 3 && (trimmed[0] == '`' || trimmed[0] == '~') {
			n := len(trimmed) - len(strings.TrimLeft(trimmed, string(trimmed[0])))
			if n >= 3 && (fence == 0 || fence == trimmed[0] && n >= width && strings.TrimSpace(trimmed[n:]) == "") {
				if fence == 0 {
					fence, width = trimmed[0], n
				} else {
					fence, width = 0, 0
				}
				out.WriteString(line)
				continue
			}
		}
		if fence != 0 || strings.HasPrefix(line, "    ") || strings.HasPrefix(line, "\t") {
			out.WriteString(line)
			continue
		}
		for i := 0; i < len(line); {
			if startOffset+i < inlineEnd {
				end := min(len(line), inlineEnd-startOffset)
				out.WriteString(line[i:end])
				i = end
				continue
			}
			if line[i] == '\\' && i+1 < len(line) {
				out.WriteString(line[i : i+2])
				i += 2
				continue
			}
			if line[i] == '`' {
				n := 1
				for i+n < len(line) && line[i+n] == '`' {
					n++
				}
				if end := closingBackticks(markdown[startOffset+i+n:], n); end >= 0 {
					inlineEnd = startOffset + i + 2*n + end
					continue
				}
			}
			if strings.HasPrefix(line[i:], "![") {
				if end := strings.Index(line[i+2:], "]("); end >= 0 && strings.HasPrefix(line[i+2+end+2:], ImageURLDir) {
					start := i + 2 + end + 2 + len(ImageURLDir)
					if close := strings.IndexByte(line[start:], ')'); close >= 0 {
						name := line[start : start+close]
						if ValidImageAsset(name) {
							if target := replace(name, line[i+2:i+2+end]); target != "" {
								out.WriteString(target)
								i = start + close + 1
								continue
							}
						}
					}
				}
			}
			out.WriteByte(line[i])
			i++
		}
	}
	return out.String()
}

func closingBackticks(text string, width int) int {
	for i := 0; i < len(text); {
		start := strings.IndexByte(text[i:], '`')
		if start < 0 {
			return -1
		}
		start += i
		end := start
		for end < len(text) && text[end] == '`' {
			end++
		}
		if end-start == width {
			return start
		}
		i = end
	}
	return -1
}

func (d Draft) mediaBody(source Source, links map[int]string, attribution bool) string {
	m := source.Media
	body := ""
	if m.linkOnly() {
		if link := links[source.MessageID]; link != "" {
			label := "Watch video"
			if m.Kind == "file" {
				label = "Open attachment"
			}
			body = "[" + label + "](" + escapeMediaURL(link) + ")"
		} else {
			body = "*" + m.linkLabel() + " · public link needed*"
		}
	} else {
		body = "![](" + ImageURLDir + m.Asset + ")"
	}
	if source.Text != "" {
		body += "\n\n" + source.Text
	}
	if attribution && m.Origin != "" && (!m.linkOnly() || links[source.MessageID] != m.Origin) {
		body += "\n\n[Source](" + escapeMediaURL(m.Origin) + ")"
	}
	return body
}

func escapeMediaURL(value string) string {
	return strings.NewReplacer("(", "%28", ")", "%29", "<", "%3C", ">", "%3E").Replace(value)
}
