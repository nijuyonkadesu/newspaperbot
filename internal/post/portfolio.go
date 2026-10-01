package post

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"
)

var slugPattern = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)
var slugSeparator = regexp.MustCompile(`[^a-z0-9]+`)

func ValidSlug(value string) bool { return slugPattern.MatchString(value) }

func Slug(title string) string {
	return strings.Trim(slugSeparator.ReplaceAllString(strings.ToLower(title), "-"), "-")
}

func ValidateCategory(value string) error {
	if value == "" || value != strings.TrimSpace(value) || strings.ContainsAny(value, "\r\n\u2028\u2029") {
		return errors.New("category must be a nonempty single-line name")
	}
	return nil
}

func (d Draft) ValidatePortfolio() error {
	if err := d.Validate(); err != nil {
		return err
	}
	if err := ValidateCategory(d.Category); err != nil {
		return err
	}
	for _, tag := range d.Tags {
		if !ValidSlug(tag) {
			return fmt.Errorf("invalid tag %q: use lowercase letters, numbers, and single hyphens", tag)
		}
	}
	return nil
}

// JSON-quoted scalars are valid YAML and keep dates, punctuation, and Unicode
// unambiguous for the portfolio's gray-matter parser.
func (d Draft) PortfolioMarkdown() ([]byte, error) {
	quote := func(s string) string { value, _ := json.Marshal(s); return string(value) }
	slug := d.Slug
	if slug == "" {
		slug = Slug(d.Title)
	}
	if slug == "" {
		slug = fmt.Sprintf("note-%d", d.ID)
	}
	date := d.PublishedAt
	if date.IsZero() {
		date = time.Now().UTC()
	}
	tags := d.Tags
	if tags == nil {
		tags = []string{}
	}
	encoded, err := json.Marshal(tags)
	if err != nil {
		return nil, err
	}
	header := fmt.Sprintf("---\ntype: tweet\ntitle: %s\nslug: %s\ndate: %s\nsummary: %s\ncategory: %s\ntags: %s\n---\n\n", quote(d.Title), quote(slug), quote(date.UTC().Format("2006-01-02")), quote(d.Summary), quote(d.Category), encoded)
	return []byte(header + d.Content), nil
}

func (d Draft) CategoryLabel() string {
	if d.Category != "" && !slices.Contains(d.Categories, d.Category) {
		return d.Category + "*"
	}
	return d.Category
}

func (d Draft) TagLabels() []string {
	labels := slices.Clone(d.Tags)
	for i, tag := range labels {
		if !slices.Contains(d.AvailableTags, tag) {
			labels[i] += "*"
		}
	}
	return labels
}

// Associated tags are suggestions, never restrictions or filtered choices.
func (d *Draft) OrderTags() {
	ordered := []string{}
	for _, tag := range d.TagGroups[d.Category] {
		if slices.Contains(d.AvailableTags, tag) && !slices.Contains(ordered, tag) {
			ordered = append(ordered, tag)
		}
	}
	for _, tag := range d.AvailableTags {
		if !slices.Contains(ordered, tag) {
			ordered = append(ordered, tag)
		}
	}
	d.AvailableTags = ordered
}
