package repository

import (
	"archive/tar"
	"context"
	"errors"
	"io"
	"slices"
	"strings"

	"newspaperbot/internal/post"
	"newspaperbot/internal/store"
)

// Articles reads one immutable Git snapshot, independent of a publishing job's
// working tree. One archive avoids a subprocess for every article.
func (r *Repository) Articles(ctx context.Context) ([]post.Article, error) {
	c, err := r.Catalog(ctx)
	if err != nil {
		return nil, err
	}
	archive, err := r.git(ctx, "archive", c.Revision, "--", "content/tweets")
	if err != nil {
		return nil, err
	}
	reader := tar.NewReader(strings.NewReader(archive))
	var articles []post.Article
	seen := map[int64]bool{}
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		if header.Typeflag != tar.TypeReg || !validNotePath(header.Name) {
			continue
		}
		data, err := io.ReadAll(reader)
		if err != nil {
			return nil, err
		}
		article, err := post.ReadArticle(header.Name, string(data))
		if errors.Is(err, post.ErrArticleDraft) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if seen[article.Number] {
			return nil, errors.New("repository contains duplicate article numbers")
		}
		seen[article.Number] = true
		articles = append(articles, article)
	}
	slices.SortFunc(articles, func(a, b post.Article) int {
		if order := b.PublishedAt.Compare(a.PublishedAt); order != 0 {
			return order
		}
		if a.Number > b.Number {
			return -1
		}
		if a.Number < b.Number {
			return 1
		}
		return 0
	})
	return articles, nil
}

func (r *Repository) checkRevision(ctx context.Context, job *store.Publication) error {
	d := job.Draft
	if d.Revision == nil {
		return nil
	}
	if !validNotePath(d.Filename) {
		return errors.New("invalid article path")
	}
	original, err := post.ReadArticle(d.Filename, d.Revision.Original)
	if err != nil {
		return err
	}
	if d.Number != original.Number || d.Slug != original.Slug || !d.PublishedAt.Equal(original.PublishedAt) {
		return errors.New("article identity cannot change during revision")
	}
	current, err := r.git(ctx, "show", "origin/main:"+d.Filename)
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	if err != nil || current != d.Revision.Original {
		// Distinguish a removed/changed article from transport failures: this
		// lookup follows a successful fetch and reads an immutable local ref.
		return post.ErrArticleChanged
	}
	return nil
}
