package post

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	Compose = "compose"
	Review  = "review"
)

// Source is an owner message contributing to a draft. Full identifies the
// title/summary/body message; other sources are verbatim body additions.
type Source struct {
	MessageID  int
	UpdateID   int64
	EditDate   int
	Text       string
	Full       bool
	ParseError string // Invalid post or taxonomy; do not publish the previous snapshot.
	Media      *Media `json:",omitempty"`
	MediaFor   int    `json:",omitempty"` // A reply supplying the public link for a video source.
}

type MessageIssue struct {
	MessageID int
	UpdateID  int64
	EditDate  int
	Reason    string
}

// Revision keeps the live source separate from pending changes and checkpoints
// channel edits after Git succeeds. It never occupies a draft slot.
type Revision struct {
	Original                                    string
	Applied, Conflict, SummaryDone, ContentDone bool
	Missing                                     bool
}

// Draft also holds publication progress so interrupted exports can be recovered.
type Draft struct {
	ID                 int64 // Stable database identity; never shown to the owner.
	Slot               int64 `json:"-"` // Reusable owner-facing number while this is a draft.
	ComposerVersion    int
	CardID             int
	SourceReplyID      int // Temporary native reply used to locate a source message.
	SourceReplyToID    int
	View               string
	Preview            bool // Preferred review mode, retained while temporary panels are open.
	Notice             string
	Invalid            string
	Sources            []Source
	MessageIssues      []MessageIssue
	ReplacementSource  *Source
	BaseContent        string
	Images             map[string]ImageRef `json:",omitempty"` // Telegram references for bot-owned assets.
	LateMedia          []Source            `json:",omitempty"`
	AlbumUpdatedAt     time.Time           `json:",omitzero"`
	PublishRequestedAt time.Time           `json:",omitzero"`

	Title            string
	Summary          string
	Content          string
	Category         string
	Tags             []string
	Categories       []string
	AvailableTags    []string
	TagGroups        map[string][]string
	Step             string
	Editing          bool   // Legacy wizard state, retained for decoding old drafts.
	PendingContent   string // Legacy unfinished body replacement, recoverable in Options.
	LastMessageID    int
	UpdatedAt        time.Time
	Number           int64
	PublishedAt      time.Time
	Filename         string
	Exported         bool
	ChannelID        int64
	Delivery         string // pending, uncertain, sent, or empty when no channel was selected
	DocumentMode     bool
	SummaryMessageID int
	ContentMessageID int
	GitOperation     string // Nonempty once a repository publication is queued.
	GitState         string // queued, failed, or done
	CommitSHA        string
	Slug             string
	Portfolio        bool
	Frontmatter      string // Existing article fields, including metadata unknown to the bot.
	Revision         *Revision
}

// Telegram can randomize update IDs after a week without updates. Edit dates
// distinguish a genuinely newer edit from an old replay across that reset.
func (s Source) NewerThan(old Source) bool {
	if s.EditDate != old.EditDate {
		return s.EditDate > old.EditDate
	}
	return s.UpdateID == 0 || s.UpdateID > old.UpdateID
}

// Normalize converts saved wizard states without discarding unfinished text.
func (d *Draft) Normalize() {
	if d.View == "preview" {
		d.Preview = true
	}
	if d.ComposerVersion != 0 {
		return
	}
	d.ComposerVersion = 1
	if d.Number != 0 {
		return
	}
	if d.Content == "" && d.PendingContent != "" {
		d.Content, d.PendingContent = d.PendingContent, ""
	}
	if d.Category == "" && len(d.Categories) > 0 {
		d.Category = d.Categories[0]
	}
	d.Editing = false
	d.Step = Compose
	if d.Validate() == nil {
		d.Step = Review
	}
}

// ResetView returns from a temporary panel to the owner's chosen review mode.
func (d *Draft) ResetView() {
	d.View = ""
	if d.Preview {
		d.View = "preview"
	}
}

// ParseSource accepts a title line, summary paragraph, and Markdown remainder.
// It consumes separators, but leaves the body's bytes (including indentation)
// alone. A failed replacement never partially changes a draft.
func ParseSource(text string) (title, summary, content string, err error) {
	text = strings.TrimLeft(text, "\r\n")
	if strings.HasPrefix(text, "/newpost") {
		first, rest, _ := strings.Cut(text, "\n")
		command, inline, _ := strings.Cut(first, " ")
		if strings.SplitN(command, "@", 2)[0] == "/newpost" {
			text = inline + "\n" + rest
			text = strings.TrimLeft(text, "\r\n")
		}
	}
	title, rest, ok := strings.Cut(text, "\n")
	title = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(title), "# "))
	rest = strings.TrimLeft(rest, "\r\n")
	// Telegram normally supplies LF; accept CRLF separators without rewriting body.
	split, size := strings.Index(rest, "\n\n"), 2
	if n := strings.Index(rest, "\r\n\r\n"); n >= 0 && (split < 0 || n < split) {
		split, size = n, 4
	}
	if !ok || split < 0 {
		return "", "", "", errors.New("send title, summary, and body together; separate summary and body with a blank line")
	}
	summary = strings.TrimSpace(rest[:split])
	content = rest[split+size:]
	if title == "" || summary == "" || strings.TrimSpace(content) == "" {
		return "", "", "", errors.New("title, summary, and body must each contain text")
	}
	if utf8.RuneCountInString(title) > 200 || utf8.RuneCountInString(summary) > 2000 {
		return "", "", "", errors.New("keep title within 200 characters and summary within 2000")
	}
	return title, summary, content, nil
}

func (d *Draft) Replace(source Source) error {
	if d.Locked() {
		return errors.New("published posts are locked")
	}
	parsed, err := parseSource(source.Text, d.Categories, d.AvailableTags)
	if err != nil {
		return err
	}
	source.Full = true
	parsed.applyTaxonomy(d, &source)
	d.Title, d.Summary, d.Content = parsed.title, parsed.summary, parsed.body
	var pending []Source
	if d.Step == Compose && d.View != "replace" {
		for _, old := range d.Sources {
			if old.Media != nil {
				pending = append(pending, old)
			}
		}
	}
	d.Sources, d.MessageIssues, d.BaseContent, d.ReplacementSource = append([]Source{source}, pending...), nil, "", nil
	d.Step, d.Notice, d.Invalid, d.PendingContent = Review, "", "", ""
	d.ResetView()
	d.PublishRequestedAt = time.Time{}
	return d.rebuild()
}

func (d *Draft) Append(source Source) error {
	if d.Locked() {
		return errors.New("published posts are locked")
	}
	if source.Media == nil && strings.TrimSpace(source.Text) == "" {
		return errors.New("send text to append to the body")
	}
	if source.Media == nil && source.MediaFor == 0 {
		d.PublishRequestedAt = time.Time{}
		parsed, err := parseFooter(source.Text, d.Categories, d.AvailableTags)
		if err != nil {
			source.ParseError = err.Error()
		} else {
			parsed.applyTaxonomy(d, &source)
		}
	}
	d.rememberImage(source.Media)
	if len(d.Sources) == 0 {
		d.BaseContent = d.Content
	}
	d.Sources = append(d.Sources, source)
	return d.rebuild()
}

func (d *Draft) EditSource(source Source) bool {
	if d.Locked() {
		return false
	}
	for i, old := range d.Sources {
		if old.MessageID != source.MessageID {
			continue
		}
		if !source.NewerThan(old) {
			return false
		}
		source.Full = old.Full
		d.PublishRequestedAt = time.Time{}
		var parsed parsedSource
		var err error
		source.MediaFor = old.MediaFor
		if old.Media != nil || source.MediaFor != 0 {
			d.rememberImage(source.Media)
			d.Sources[i] = source
			_ = d.rebuild()
			return true
		}
		if source.Full {
			parsed, err = parseSource(source.Text, d.Categories, d.AvailableTags)
		} else {
			parsed, err = parseFooter(source.Text, d.Categories, d.AvailableTags)
		}
		if err != nil {
			source.ParseError = err.Error()
		} else {
			parsed.applyTaxonomy(d, &source)
		}
		d.Sources[i] = source
		_ = d.rebuild() // Invalid source is retained; the last valid post is preserved.
		return true
	}
	return false
}

func (d *Draft) Undo() error {
	if d.Locked() || len(d.Sources) == 0 || d.Sources[len(d.Sources)-1].Full {
		return errors.New("there is no appended message to undo")
	}
	d.PublishRequestedAt = time.Time{}
	messageID := d.Sources[len(d.Sources)-1].MessageID
	d.Sources = d.Sources[:len(d.Sources)-1]
	d.MessageIssues = slices.DeleteFunc(d.MessageIssues, func(issue MessageIssue) bool { return issue.MessageID == messageID })
	return d.rebuild()
}

func (d *Draft) RemoveSource(messageID int) error {
	for i, source := range d.LateMedia {
		if source.MessageID == messageID {
			d.LateMedia = slices.Delete(d.LateMedia, i, i+1)
			d.MessageIssues = slices.DeleteFunc(d.MessageIssues, func(issue MessageIssue) bool { return issue.MessageID == messageID })
			return nil
		}
	}
	if d.Locked() {
		return errors.New("published posts are locked")
	}
	d.PublishRequestedAt = time.Time{}
	for _, source := range d.Sources {
		if source.MessageID == messageID && source.Full {
			return errors.New("Original post · use /replace or /cancel")
		}
	}

	found, sourceRemoved := false, false
	d.Sources = slices.DeleteFunc(d.Sources, func(source Source) bool {
		remove := source.MessageID == messageID || source.MediaFor == messageID
		found = found || remove
		sourceRemoved = sourceRemoved || remove
		return remove
	})
	d.MessageIssues = slices.DeleteFunc(d.MessageIssues, func(issue MessageIssue) bool {
		remove := issue.MessageID == messageID
		found = found || remove
		return remove
	})
	if d.ReplacementSource != nil && d.ReplacementSource.MessageID == messageID {
		d.ReplacementSource, found = nil, true
	}
	if !found {
		return errors.New("Source not found in this draft")
	}
	if sourceRemoved {
		_ = d.rebuild() // Another invalid source may still require review.
	} else {
		d.Notice = ""
	}
	d.ResetView()
	return nil
}

func (d *Draft) rebuild() error {
	return d.rebuildWithCaptions(false)
}

func (d *Draft) rebuildWithCaptions(captionBlocks bool) error {
	d.Notice = ""
	title, summary, body := d.Title, d.Summary, d.BaseContent
	links, groups := d.mediaLinks(), map[string]bool{}
	for _, source := range d.Sources {
		if source.MediaFor != 0 {
			continue
		}
		if source.ParseError != "" {
			d.Invalid = "Fix the source message: " + source.ParseError
			return errors.New(source.ParseError)
		}
		if source.Media != nil {
			group := source.Media.GroupID
			if group != "" && groups[group] {
				continue
			}
			members := []Source{source}
			if group != "" {
				groups[group] = true
				members = nil
				for _, member := range d.Sources {
					if member.Media != nil && member.Media.GroupID == group {
						members = append(members, member)
					}
				}
				slices.SortFunc(members, func(a, b Source) int { return a.MessageID - b.MessageID })
			}
			for i, member := range members {
				if body != "" {
					body += "\n\n"
				}
				body += d.mediaBody(member, links, group == "" || i == len(members)-1, captionBlocks)
			}
		} else if source.Full {
			var err error
			title, summary, body, err = ParseSource(source.Text)
			if err != nil {
				d.Invalid = "Fix the edited post message: " + err.Error()
				return err
			}
		} else {
			if strings.TrimSpace(source.Text) == "" {
				d.Invalid = "An edited body message is empty. Fix it or undo the last addition."
				return errors.New(d.Invalid)
			}
			if body != "" {
				body += "\n\n"
			}
			body += source.Text
		}
	}
	d.Title, d.Summary, d.Content = title, summary, body
	d.Invalid, d.Notice, d.Step = "", "", Review
	if d.Title == "" {
		d.Step = Compose
	}
	d.ResetView()
	return nil
}

func (d Draft) Validate() error {
	if d.Invalid != "" {
		return errors.New(d.Invalid)
	}
	if strings.TrimSpace(d.Title) == "" || d.Number == 0 && strings.TrimSpace(d.Summary) == "" || strings.TrimSpace(d.Content) == "" || d.Category == "" {
		return errors.New("send title, summary, and body together before publishing")
	}
	return d.ValidateMedia()
}

func (d Draft) Empty() bool {
	return !d.Locked() && d.Title == "" && d.Summary == "" && d.Content == "" && d.PendingContent == "" && len(d.Sources) == 0 && d.ReplacementSource == nil
}

func (d Draft) Locked() bool {
	return d.GitOperation != "" || d.Number != 0 && (d.Revision == nil || d.Revision.Applied)
}

func (d Draft) RichMarkdown() string {
	return "# " + d.Title + "\n\n" + d.Summary + "\n\n" + d.TelegramContent()
}

func (d Draft) Markdown() ([]byte, error) {
	if d.Portfolio {
		return d.PortfolioMarkdown()
	}
	var date *time.Time
	if !d.PublishedAt.IsZero() {
		date = &d.PublishedAt
	}
	frontmatter := struct {
		Number   int64      `json:"number,omitempty"`
		Title    string     `json:"title"`
		Summary  string     `json:"summary"`
		Category string     `json:"category"`
		Tags     []string   `json:"tags"`
		Date     *time.Time `json:"date,omitempty"`
	}{d.Number, d.Title, d.Summary, d.Category, d.Tags, date}
	data, err := json.MarshalIndent(frontmatter, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(append(data, '\n', '\n'), []byte(d.exportContent())...), nil
}
