package repository

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"
	"tgblogbot/internal/post"
	"tgblogbot/internal/store"
)

// Publish commits first, then fetches/rebases before a normal fast-forward push.
// If generated files conflict or numbering collides, rebuild from the newest
// remote instead of trying to merge generated YAML. Three attempts bound races.
func (r *Repository) Publish(ctx context.Context, job *store.Publication, checkpoint func() error) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := job.Draft.ValidatePortfolio(); err != nil {
		return err
	}
	for attempt := 0; attempt < 3; attempt++ {
		if err := r.fetch(ctx); err != nil {
			return err
		}
		if found, err := r.recoverRemote(ctx, job); err != nil {
			return err
		} else if found {
			return checkpoint()
		}
		// Only this marked, exclusively locked cache is disposable. Preserve
		// unrelated untracked files; remove only this job's checkpointed note.
		_, _ = r.git(ctx, "rebase", "--abort")
		if job.Filename != "" {
			if !validNotePath(job.Filename) {
				return errors.New("invalid checkpointed note path")
			}
			if _, err := r.git(ctx, "clean", "-f", "--", job.Filename); err != nil {
				return err
			}
		}
		if _, err := r.git(ctx, "reset", "--hard", "origin/main"); err != nil {
			return err
		}
		if err := r.install(ctx); err != nil {
			return err
		}
		if err := r.prepare(ctx, job, checkpoint); err != nil {
			return err
		}
		if err := r.fetch(ctx); err != nil {
			return err
		}
		if found, err := r.recoverRemote(ctx, job); err != nil {
			return err
		} else if found {
			return checkpoint()
		}
		if _, err := r.git(ctx, "rebase", "origin/main"); err != nil {
			_, _ = r.git(ctx, "rebase", "--abort")
			continue
		}
		// A clean textual rebase can still produce duplicate numeric prefixes
		// in different filenames. The portfolio loader validates the whole set.
		if err := r.install(ctx); err != nil {
			return err
		}
		if _, err := r.command(ctx, "npm", "run", "taxonomy:sync"); err != nil {
			continue
		}
		paths, err := r.taxonomyPaths()
		if err != nil {
			return err
		}
		if err := r.stage(ctx, job.Filename, paths); err != nil {
			return err
		}
		diff, err := r.git(ctx, "diff", "--cached", "--name-only")
		if err != nil {
			return err
		}
		if strings.TrimSpace(diff) != "" {
			if _, err := r.git(ctx, "commit", "--amend", "--no-edit"); err != nil {
				return err
			}
		}
		sha, err := r.git(ctx, "rev-parse", "HEAD")
		if err != nil {
			return err
		}
		job.CommitSHA = strings.TrimSpace(sha)
		if err := checkpoint(); err != nil {
			return err
		} // Durable before the network side effect.
		if _, err := r.git(ctx, "push", "origin", "HEAD:refs/heads/main"); err != nil {
			// A failed response may have followed a successful push. The next
			// fetch checks the operation trailer before creating another note.
			continue
		}
		job.State, job.Error = "pushed", ""
		if err := checkpoint(); err != nil {
			return err
		}
		// Update the source snapshot only after the remote contains the commit.
		if err := r.fetch(ctx); err == nil {
			_ = r.readCatalog(ctx)
		}
		return nil
	}
	return errors.New("could not confirm publication after three attempts; retry publishing to check the remote and continue")
}

func validNotePath(name string) bool {
	return filepath.Dir(name) == "content/tweets" && filepath.Base(name) != ".md" && strings.HasSuffix(name, ".md") && !strings.ContainsAny(name, "\r\n")
}

func (r *Repository) prepare(ctx context.Context, job *store.Publication, checkpoint func() error) error {
	entries, err := os.ReadDir(filepath.Join(r.config.CacheDir, "content", "tweets"))
	if err != nil {
		return err
	}
	var highest int64
	slugs := map[string]bool{}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}
		prefix, _, ok := strings.Cut(entry.Name(), "-")
		if ok {
			n, err := strconv.ParseInt(prefix, 10, 64)
			if err != nil || n < 1 || n >= 9007199254740991 {
				return fmt.Errorf("invalid note number in %q", entry.Name())
			}
			highest = max(highest, n)
		}
		data, err := os.ReadFile(filepath.Join(r.config.CacheDir, "content", "tweets", entry.Name()))
		if err != nil {
			return err
		}
		var front struct {
			Slug string `yaml:"slug"`
		}
		if err := frontmatter(data, &front); err != nil {
			return fmt.Errorf("%s: %w", entry.Name(), err)
		}
		slugs[front.Slug] = true
	}
	job.Number = highest + 1
	slug := post.Slug(job.Draft.Title)
	if slug == "" {
		slug = fmt.Sprintf("note-%d", job.Number)
	}
	base := slug
	for suffix := 2; slugs[slug]; suffix++ {
		slug = fmt.Sprintf("%s-%d", base, suffix)
	}
	job.Slug, job.CommitSHA = slug, ""
	job.Filename = fmt.Sprintf("content/tweets/%03d-%s.md", job.Number, slug)
	if err := checkpoint(); err != nil {
		return err
	} // Recover even a crash after writing an untracked note.
	d := job.Draft
	d.Number, d.Slug, d.Portfolio = job.Number, job.Slug, true
	data, err := d.PortfolioMarkdown()
	if err != nil {
		return err
	}
	if err := post.WriteFile(filepath.Join(r.config.CacheDir, job.Filename), data); err != nil {
		return err
	}
	if _, err := r.command(ctx, "npm", "run", "taxonomy:sync"); err != nil {
		return err
	}
	paths, err := r.taxonomyPaths()
	if err != nil {
		return err
	}
	if err := r.stage(ctx, job.Filename, paths); err != nil {
		return err
	}
	message := fmt.Sprintf("Publish note %d: %s\n\nBlogbot-Operation: %s", job.Number, strings.ReplaceAll(job.Draft.Title, "\n", " "), job.Operation)
	if _, err := r.git(ctx, "commit", "-m", message); err != nil {
		return err
	}
	sha, err := r.git(ctx, "rev-parse", "HEAD")
	if err != nil {
		return err
	}
	job.CommitSHA = strings.TrimSpace(sha)
	return checkpoint()
}

func (r *Repository) taxonomyPaths() ([]string, error) {
	var paths []string
	for _, stem := range []string{"category", "tag"} {
		for _, ext := range []string{".yaml", ".txt"} {
			name := stem + ext
			if info, err := os.Stat(filepath.Join(r.config.CacheDir, name)); err == nil && info.Mode().IsRegular() {
				paths = append(paths, name)
				break
			}
		}
	}
	if len(paths) != 2 {
		return nil, errors.New("taxonomy generator must produce both reference files")
	}
	return paths, nil
}

func (r *Repository) stage(ctx context.Context, note string, taxonomy []string) error {
	allowed := append([]string{note}, taxonomy...)
	changed, err := r.git(ctx, "diff", "HEAD", "--name-only")
	if err != nil {
		return err
	}
	for _, name := range strings.Fields(changed) {
		if !slices.Contains(allowed, name) {
			return fmt.Errorf("taxonomy command changed unexpected file %q", name)
		}
	}
	_, err = r.git(ctx, append([]string{"add", "--"}, allowed...)...)
	return err
}

func (r *Repository) recoverRemote(ctx context.Context, job *store.Publication) (bool, error) {
	sha, err := r.git(ctx, "log", "origin/main", "--format=%H", "--fixed-strings", "--grep=Blogbot-Operation: "+job.Operation, "-1")
	if err != nil {
		return false, err
	}
	sha = strings.TrimSpace(sha)
	if sha == "" {
		return false, nil
	}
	files, err := r.git(ctx, "diff-tree", "--no-commit-id", "--name-only", "-r", sha, "--", "content/tweets")
	if err != nil {
		return false, err
	}
	names := strings.Fields(files)
	if len(names) != 1 || !validNotePath(names[0]) {
		return false, errors.New("publication commit has an unexpected note set")
	}
	name := names[0]
	prefix, _, _ := strings.Cut(filepath.Base(name), "-")
	number, err := strconv.ParseInt(prefix, 10, 64)
	if err != nil || number < 1 {
		return false, errors.New("publication commit has an invalid number")
	}
	data, err := r.git(ctx, "show", sha+":"+name)
	if err != nil {
		return false, err
	}
	var front struct {
		Slug string `yaml:"slug"`
	}
	if err := frontmatter([]byte(data), &front); err != nil {
		return false, err
	}
	job.CommitSHA, job.Filename, job.Number, job.Slug, job.State, job.Error = sha, name, number, front.Slug, "pushed", ""
	_ = r.readCatalog(ctx)
	return true, nil
}

func frontmatter(data []byte, target any) error {
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	if !strings.HasPrefix(text, "---\n") {
		return errors.New("expected YAML frontmatter")
	}
	front, _, ok := strings.Cut(text[4:], "\n---")
	if !ok {
		return errors.New("unterminated YAML frontmatter")
	}
	return yaml.Unmarshal([]byte(front), target)
}
