package repository

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"newspaperbot/internal/post"
	"newspaperbot/internal/store"
)

func revisionJob(t *testing.T, f fixture) store.Publication {
	t.Helper()
	articles, err := f.repo.Articles(context.Background())
	if err != nil || len(articles) != 1 {
		t.Fatal("could not load live article", err)
	}
	d := articles[0].Draft
	d.Revision = &post.Revision{Original: articles[0].Original}
	d.Title, d.Category, d.Tags, d.Content = "Revised title", "research", []string{"go", "new-tag"}, "Revised body\n"
	return store.Publication{Operation: "11111111111111111111111111111111", State: "queued", Draft: d}
}

func TestRevisionUpdatesSameArticleAndRegeneratesTaxonomy(t *testing.T) {
	f := newFixture(t)
	job := revisionJob(t, f)
	beforeDate, beforeSlug, beforeFilename := job.Draft.PublishedAt, job.Draft.Slug, job.Draft.Filename
	if err := f.repo.Publish(context.Background(), &job, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	raw := command(t, f.remote, "git", "show", "main:"+beforeFilename)
	article, err := post.ReadArticle(beforeFilename, raw)
	if err != nil {
		t.Fatal(err)
	}
	if article.Number != 268 || job.Number != 268 || job.Filename != beforeFilename || article.Slug != beforeSlug || !article.PublishedAt.Equal(beforeDate) || article.Title != "Revised title" {
		t.Fatalf("revision changed identity: %+v", article)
	}
	if names := command(t, f.remote, "git", "ls-tree", "--name-only", "main", "content/tweets/"); names != beforeFilename {
		t.Fatal("revision created another article", names)
	}
	groups := command(t, f.remote, "git", "show", "main:tag.yaml")
	if !strings.Contains(groups, `"research": ["go", "new-tag"]`) || strings.Contains(groups, "concept") {
		t.Fatal("taxonomy was not rebuilt from live articles", groups)
	}
	head := command(t, f.remote, "git", "rev-parse", "main")
	job.State, job.CommitSHA = "queued", ""
	if err := f.repo.Publish(context.Background(), &job, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	if command(t, f.remote, "git", "rev-parse", "main") != head {
		t.Fatal("retry created another revision commit")
	}
}

func TestRevisionDetectsConcurrentChangesAndPreservesOtherWriters(t *testing.T) {
	for _, sameArticle := range []bool{false, true} {
		t.Run(map[bool]string{false: "unrelated", true: "same-article"}[sameArticle], func(t *testing.T) {
			f := newFixture(t)
			job := revisionJob(t, f)
			raced := false
			err := f.repo.Publish(context.Background(), &job, func() error {
				if raced || job.CommitSHA == "" {
					return nil
				}
				raced = true
				name, text := "README.md", "Other writer\n"
				if sameArticle {
					name, text = job.Draft.Filename, strings.Replace(job.Draft.Revision.Original, "Body", "Remote body", 1)
				}
				write(t, filepath.Join(f.writer, name), text)
				command(t, f.writer, "git", "add", name)
				command(t, f.writer, "git", "commit", "-m", "Other writer")
				command(t, f.writer, "git", "push", "origin", "main")
				return nil
			})
			if sameArticle {
				if !errors.Is(err, post.ErrArticleChanged) {
					t.Fatal("same-article conflict not detected", err)
				}
				if raw := command(t, f.remote, "git", "show", "main:"+job.Draft.Filename); !strings.Contains(raw, "Remote body") || strings.Contains(raw, "Revised body") {
					t.Fatal("overwrote remote article")
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				if command(t, f.remote, "git", "show", "main:README.md") != "Other writer" {
					t.Fatal("lost unrelated update")
				}
			}
		})
	}
}
