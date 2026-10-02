package post

import (
	"bytes"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"
)

var ErrArticleChanged = errors.New("article changed on main; discard and reload to review the latest version")
var ErrArticleDraft = errors.New("article is marked as a draft")

type Article struct {
	Draft
	Original string
}

func ReadArticle(filename, text string) (Article, error) {
	a := Article{Original: text}
	text = strings.ReplaceAll(text, "\r\n", "\n")
	front, body, ok := strings.Cut(strings.TrimPrefix(text, "---\n"), "\n---\n")
	if !strings.HasPrefix(text, "---\n") || !ok {
		return a, errors.New("article requires YAML frontmatter")
	}
	var fields struct {
		Title, Summary, Slug, Date, Category string
		Tags                                 []string
		Draft                                bool
	}
	if err := yaml.Unmarshal([]byte(front), &fields); err != nil {
		return a, err
	}
	if fields.Draft {
		return a, ErrArticleDraft
	}
	prefix, _, ok := strings.Cut(filepath.Base(filename), "-")
	number, err := strconv.ParseInt(prefix, 10, 64)
	if !ok || err != nil || number < 1 || !ValidSlug(fields.Slug) {
		return a, errors.New("article requires a numeric filename and valid slug")
	}
	date, err := time.Parse("2006-01-02", fields.Date)
	if err != nil {
		return a, fmt.Errorf("article date: %w", err)
	}
	category := strings.TrimSpace(fields.Category)
	if category == "" {
		category = "uncategorized"
	}
	a.Draft = Draft{ComposerVersion: 1, Step: Review, Portfolio: true, Exported: true,
		Number: number, Filename: filename, Slug: fields.Slug, PublishedAt: date,
		Title: fields.Title, Summary: fields.Summary, Content: strings.TrimPrefix(body, "\n"),
		Category: category, Tags: fields.Tags, Frontmatter: front}
	return a, nil
}

// SameArticle compares the author-editable fields of two article versions.
func SameArticle(a, b Draft) bool {
	return a.Title == b.Title && a.Summary == b.Summary && a.Content == b.Content &&
		a.Category == b.Category && slices.Equal(a.Tags, b.Tags)
}

// Existing metadata is retained; only author-editable fields are replaced.
func (d Draft) articleMarkdown() ([]byte, error) {
	if d.Revision != nil {
		original, err := ReadArticle(d.Filename, d.Revision.Original)
		if err != nil {
			return nil, err
		}
		if d.Number != original.Number || d.Slug != original.Slug || !d.PublishedAt.Equal(original.PublishedAt) {
			return nil, errors.New("article identity cannot change during revision")
		}
		if SameArticle(d, original.Draft) {
			return []byte(d.Revision.Original), nil
		}
	}
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(d.Frontmatter), &doc); err != nil {
		return nil, err
	}
	if len(doc.Content) != 1 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, errors.New("article frontmatter must be a mapping")
	}
	fields := doc.Content[0]
	tags := d.Tags
	if tags == nil {
		tags = []string{}
	}
	for _, field := range []struct {
		name  string
		value any
	}{
		{"title", d.Title}, {"summary", d.Summary}, {"category", d.Category}, {"tags", tags},
	} {
		var value yaml.Node
		if err := value.Encode(field.value); err != nil {
			return nil, err
		}
		if value.Kind == yaml.ScalarNode {
			value.Style = yaml.DoubleQuotedStyle
		} else {
			value.Style = yaml.FlowStyle
			for _, tag := range value.Content {
				tag.Style = yaml.DoubleQuotedStyle
			}
		}
		found := false
		for i := 0; i < len(fields.Content); i += 2 {
			if fields.Content[i].Value == field.name {
				old := fields.Content[i+1]
				value.HeadComment, value.LineComment, value.FootComment = old.HeadComment, old.LineComment, old.FootComment
				fields.Content[i+1], found = &value, true
				break
			}
		}
		if !found {
			fields.Content = append(fields.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: field.name}, &value)
		}
	}
	var out bytes.Buffer
	out.WriteString("---\n")
	encoder := yaml.NewEncoder(&out)
	encoder.SetIndent(2)
	if err := encoder.Encode(&doc); err != nil {
		return nil, err
	}
	if err := encoder.Close(); err != nil {
		return nil, err
	}
	out.WriteString("---\n\n")
	out.WriteString(d.Content)
	return out.Bytes(), nil
}
