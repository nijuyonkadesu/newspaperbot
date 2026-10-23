package repository

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	"newspaperbot/internal/post"
	"newspaperbot/internal/store"
)

func TestOriginNumberCanBeReusedAfterExternalRemoval(t *testing.T) {
	for _, removal := range []string{"revert", "delete"} {
		t.Run(removal, func(t *testing.T) {
			ctx := context.Background()
			f := newFixture(t)
			path := filepath.Join(t.TempDir(), "state.db")
			s, err := store.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { s.Close() }()
			publish := func(channel int64) post.Draft {
				t.Helper()
				d, err := s.New(ctx, []string{"concept"}, []string{"go"})
				if err != nil {
					t.Fatal(err)
				}
				d.Title, d.Summary, d.Content = "Same title", "Summary", "Body"
				if err := s.Save(ctx, &d); err != nil {
					t.Fatal(err)
				}
				if _, err := s.Queue(ctx, d.ID, channel); err != nil {
					t.Fatal(err)
				}
				jobs, err := s.PendingPublications(ctx)
				if err != nil || len(jobs) != 1 {
					t.Fatalf("pending publications: %v, %v", jobs, err)
				}
				job := &jobs[0]
				if err := f.repo.Publish(ctx, job, func() error { return s.SavePublication(ctx, job) }); err != nil {
					t.Fatal(err)
				}
				d, err = s.FinishPublication(ctx, job)
				if err != nil {
					t.Fatal(err)
				}
				return d
			}
			original := publish(-1001)
			original.ContentMessageID, original.Delivery = 501, "sent"
			if err := s.Save(ctx, &original); err != nil {
				t.Fatal(err)
			}
			command(t, f.writer, "git", "pull", "--ff-only")
			if removal == "revert" {
				command(t, f.writer, "git", "revert", "--no-edit", original.CommitSHA)
			} else {
				command(t, f.writer, "git", "rm", "--", original.Filename)
				command(t, f.writer, "npm", "run", "taxonomy:sync")
				command(t, f.writer, "git", "add", "category.yaml", "tag.yaml")
				command(t, f.writer, "git", "commit", "-m", "Remove published article")
			}
			command(t, f.writer, "git", "push", "origin", "main")
			// The repository cache and SQLite still remember the old publication.
			// Publishing must fetch origin and accept its number, not either cache.
			replacement := publish(-2002)
			if original.Number != 269 || replacement.Number != 269 || replacement.ID == original.ID || replacement.ChannelID != -2002 || replacement.ContentMessageID != 0 {
				t.Fatalf("origin number or delivery identity was not respected: %+v", replacement)
			}
			if err := s.Save(ctx, &original); err == nil {
				t.Fatal("stale publication overwrote the replacement association")
			}
			s.Close()
			s, err = store.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			current, err := s.GetArticle(ctx, 269)
			if err != nil || current.ID != replacement.ID || current.ChannelID != -2002 || current.ContentMessageID != 0 {
				t.Fatalf("restart restored stale delivery state: %+v, %v", current, err)
			}
			if _, err := s.Get(ctx, original.ID); !errors.Is(err, sql.ErrNoRows) {
				t.Fatal("retired publication remained actionable", err)
			}
			command(t, f.writer, "git", "pull", "--ff-only")
			command(t, f.writer, "npm", "run", "taxonomy:sync")
			if command(t, f.writer, "git", "status", "--porcelain") != "" {
				t.Fatal("replacement left stale generated taxonomy")
			}
		})
	}
}

func TestRecoveryDoesNotConfirmARevertedPublicationFromGitHistory(t *testing.T) {
	for _, reuse := range []bool{false, true} {
		t.Run(map[bool]string{false: "removed", true: "filename reused"}[reuse], func(t *testing.T) {
			ctx := context.Background()
			f := newFixture(t)
			job := newJob()
			if err := f.repo.Publish(ctx, &job, func() error {
				if job.State == "pushed" {
					return errors.New("lost completion checkpoint")
				}
				return nil
			}); err == nil {
				t.Fatal("lost checkpoint was not simulated")
			}
			command(t, f.writer, "git", "pull", "--ff-only")
			command(t, f.writer, "git", "revert", "--no-edit", job.CommitSHA)
			if reuse {
				f.addNote(t, job.Number, job.Slug, "concept", []string{"go"})
				command(t, f.writer, "git", "add", ".")
				command(t, f.writer, "git", "commit", "-m", "Different article with the same filename")
			}
			command(t, f.writer, "git", "push", "origin", "main")
			head := command(t, f.remote, "git", "rev-parse", "main")
			job.State = "queued"
			if err := f.repo.Publish(ctx, &job, func() error { return nil }); !errors.Is(err, post.ErrArticleChanged) {
				t.Fatalf("historical commit was mistaken for a current publication: %v", err)
			}
			if got := command(t, f.remote, "git", "rev-parse", "main"); got != head {
				t.Fatal("recovery recreated an article removed on origin")
			}
		})
	}
}
