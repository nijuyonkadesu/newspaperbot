package post

import (
	"errors"
	"fmt"
	"slices"
	"strings"
)

type parsedSource struct {
	title, summary, body, category string
	tags                           []string
	taxonomy                       bool
}

// A footer is opt-in: paired labels, or two final lines that both match the
// catalog. Never interpret the contents of an unclosed fenced code block.
func parseSource(text string, categories, availableTags []string) (parsedSource, error) {
	title, summary, body, err := ParseSource(text)
	if err != nil {
		return parsedSource{}, err
	}
	p, err := parseFooter(body, categories, availableTags)
	p.title, p.summary = title, summary
	return p, err
}

func parseFooter(text string, categories, availableTags []string) (parsedSource, error) {
	p := parsedSource{body: text}
	body := strings.TrimRight(text, "\r\n")
	last := strings.LastIndex(body, "\n")
	if last < 0 {
		return p, nil
	}
	previous := strings.LastIndex(body[:last], "\n")
	categoryLine, tagsLine := body[previous+1:last], body[last+1:]
	for _, line := range []string{categoryLine, tagsLine} {
		if strings.HasPrefix(line, "    ") || strings.HasPrefix(line, "\t") {
			return p, nil
		}
	}
	categoryLine, tagsLine = strings.TrimSpace(categoryLine), strings.TrimSpace(tagsLine)
	if insideFence(body[:previous+1]) {
		return p, nil
	}
	category, categoryLabel := footerValue(categoryLine, "Category")
	tagsText, tagsLabel := footerValue(tagsLine, "Tags")
	explicit := categoryLabel && tagsLabel
	if categoryLabel != tagsLabel {
		return p, nil
	}
	if !slices.Contains(categories, category) {
		if !explicit {
			return p, nil
		}
		if err := ValidateCategory(category); err != nil {
			return p, err
		}
	}
	tags := []string{}
	if tagsText != "-" {
		for _, raw := range strings.Split(tagsText, ",") {
			tag := strings.TrimSpace(raw)
			if !slices.Contains(availableTags, tag) {
				if !explicit {
					return p, nil
				}
				if !ValidSlug(tag) {
					return p, fmt.Errorf("tag %q must use lowercase letters, numbers, and single hyphens; use - for none", tag)
				}
			}
			if !slices.Contains(tags, tag) {
				tags = append(tags, tag)
			}
		}
	}
	content := strings.TrimRight(body[:previous+1], "\r\n")
	if strings.TrimSpace(content) == "" {
		return p, errors.New("add body text before the category and tags footer")
	}
	p.body, p.category, p.tags, p.taxonomy = content, category, tags, true
	return p, nil
}

func footerValue(line, label string) (string, bool) {
	prefix, value, found := strings.Cut(line, ":")
	if found && strings.EqualFold(strings.TrimSpace(prefix), label) {
		return strings.TrimSpace(value), true
	}
	return line, false
}

func insideFence(text string) bool {
	var marker byte
	var width int
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSuffix(line, "\r")
		indent := len(line) - len(strings.TrimLeft(line, " "))
		if indent > 3 {
			continue
		}
		line = line[indent:]
		if len(line) < 3 || line[0] != '`' && line[0] != '~' {
			continue
		}
		n := 1
		for n < len(line) && line[n] == line[0] {
			n++
		}
		if n < 3 {
			continue
		}
		if marker == 0 {
			marker, width = line[0], n
		} else if line[0] == marker && n >= width && strings.TrimSpace(line[n:]) == "" {
			marker, width = 0, 0
		}
	}
	return marker != 0
}

func (p parsedSource) applyTaxonomy(d *Draft, source *Source) {
	if !p.taxonomy {
		return
	}
	d.Category, d.Tags = p.category, p.tags
	// Keep metadata out of the saved body source. Appends must not reapply an
	// old footer over subsequent category/tag selections made on the card.
	source.Text = p.body
	if source.Full {
		source.Text = p.title + "\n\n" + p.summary + "\n\n" + p.body
	}
}
