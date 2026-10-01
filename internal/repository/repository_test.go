package repository

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"tgblogbot/internal/post"
	"tgblogbot/internal/store"
)

func command(t *testing.T, dir, name string, args ...string) string {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_COUNT=0", "GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@example.test", "GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@example.test")
	data, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %v: %v\n%s", name, args, err, data)
	}
	return strings.TrimSpace(string(data))
}

func write(t *testing.T, name, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(name), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}

// This small repository fixture exercises the actual git/npm subprocesses with
// no network dependencies. Its generator validates numbering independently and
// produces reference files solely from the committed note frontmatter.
const generator = `import fs from 'node:fs';
const groups = new Map(), numbers = new Set(), slugs = new Set();
for (const file of fs.readdirSync('content/tweets').filter(f => f.endsWith('.md'))) {
  const text = fs.readFileSync('content/tweets/'+file, 'utf8');
  if (!text.startsWith('---\ntype: tweet\n')) throw Error('missing YAML frontmatter');
  const front = text.split('\n---\n')[0];
  const value = key => JSON.parse(front.match(new RegExp('^'+key+': (.+)$', 'm'))[1]);
  const number = Number(file.split('-')[0]), slug = value('slug');
  if (numbers.has(number) || slugs.has(slug)) throw Error('duplicate number/slug');
  if (!/^\d{4}-\d{2}-\d{2}$/.test(value('date'))) throw Error('invalid date');
  numbers.add(number); slugs.add(slug);
  const cat = value('category'), tags = value('tags');
  if (!groups.has(cat)) groups.set(cat, new Set());
  tags.forEach(tag => groups.get(cat).add(tag));
}
const categories = [...groups.keys()].sort();
fs.writeFileSync('category.yaml', categories.map(c => '- '+JSON.stringify(c)).join('\n')+'\n');
fs.writeFileSync('tag.yaml', categories.map(c => JSON.stringify(c)+': ['+[...groups.get(c)].sort().map(t=>JSON.stringify(t)).join(', ')+']').join('\n')+'\n');
`

type fixture struct {
	remote, writer, cache string
	repo                  *Repository
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	for _, tool := range []string{"git", "node", "npm"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Fatalf("integration tests require %s", tool)
		}
	}
	dir := t.TempDir()
	f := fixture{remote: filepath.Join(dir, "remote.git"), writer: filepath.Join(dir, "writer"), cache: filepath.Join(dir, "cache")}
	command(t, dir, "git", "init", "--bare", "--initial-branch=main", f.remote)
	command(t, dir, "git", "clone", f.remote, f.writer)
	write(t, filepath.Join(f.writer, "package.json"), `{"name":"fixture","version":"1.0.0","type":"module","scripts":{"taxonomy:sync":"node sync.mjs"}}`)
	write(t, filepath.Join(f.writer, "package-lock.json"), `{"name":"fixture","version":"1.0.0","lockfileVersion":3,"packages":{"":{"name":"fixture","version":"1.0.0"}}}`)
	write(t, filepath.Join(f.writer, "sync.mjs"), generator)
	f.addNote(t, 268, "original", "concept", []string{"go"})
	command(t, f.writer, "git", "add", ".")
	command(t, f.writer, "git", "commit", "-m", "Initial portfolio")
	command(t, f.writer, "git", "push", "origin", "main")
	r, err := open(context.Background(), Config{URL: f.remote, CacheDir: f.cache, Token: "test-token-never-persist"})
	if err != nil {
		t.Fatal(err)
	}
	f.repo = r
	t.Cleanup(func() {
		if f.repo != nil {
			f.repo.Close()
		}
	})
	return f
}

func (f fixture) addNote(t *testing.T, number int64, slug, category string, tags []string) {
	t.Helper()
	d := post.Draft{Title: slug, Summary: "Summary", Content: "Body\n", Category: category, Tags: tags, Slug: slug, PublishedAt: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)}
	data, err := d.PortfolioMarkdown()
	if err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(f.writer, "content/tweets", fmtNote(number, slug)), string(data))
	command(t, f.writer, "npm", "run", "taxonomy:sync")
}

func fmtNote(number int64, slug string) string { return fmt.Sprintf("%03d-%s.md", number, slug) }

func newJob() store.Publication {
	return store.Publication{Operation: "0123456789abcdef0123456789abcdef", State: "queued", Draft: post.Draft{ID: 1, Title: "A new note", Summary: "Summary: \"quoted\"", Content: "## Body\n\n    indented\n", Category: "concept", Tags: []string{"go"}, PublishedAt: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)}}
}

func TestPublishAndRecoverWithoutDuplicate(t *testing.T) {
	f := newFixture(t)
	c, err := f.repo.Catalog(context.Background())
	if err != nil || c.LastNumber != 268 || len(c.Groups["concept"]) != 1 {
		t.Fatalf("catalog: %+v %v", c, err)
	}
	job := newJob()
	job.Draft.Category, job.Draft.Tags = "new category 日本語", []string{"new-tag", "go"}
	if err := f.repo.Publish(context.Background(), &job, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	if job.State != "pushed" || job.Number != 269 || job.Filename != "content/tweets/269-a-new-note.md" {
		t.Fatalf("result %+v", job)
	}
	changed := command(t, f.remote, "git", "diff-tree", "--no-commit-id", "--name-only", "-r", job.CommitSHA)
	for _, name := range []string{job.Filename, "category.yaml", "tag.yaml"} {
		if !strings.Contains(changed, name) {
			t.Fatalf("missing %s in %s", name, changed)
		}
	}
	head := command(t, f.remote, "git", "rev-parse", "main")
	job.CommitSHA, job.Number, job.Filename, job.State = "", 0, "", "queued" // Simulate lost local checkpoint.
	f.repo.Close()
	f.repo = nil
	r, err := open(context.Background(), Config{URL: f.remote, CacheDir: f.cache, Token: "test-token-never-persist"})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if err := r.Publish(context.Background(), &job, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	if job.Number != 269 || command(t, f.remote, "git", "rev-parse", "main") != head {
		t.Fatal("recovery republished the post")
	}
	config, err := os.ReadFile(filepath.Join(f.cache, ".git/config"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(config), "test-token") || strings.Contains(string(config), "extraheader") {
		t.Fatal("credentials persisted in Git config")
	}
}

func TestRaces(t *testing.T) {
	for _, kind := range []string{"unrelated", "number-only", "generated-taxonomy"} {
		t.Run(kind, func(t *testing.T) {
			f := newFixture(t)
			job := newJob()
			raced := false
			checkpoint := func() error {
				if job.CommitSHA == "" || raced {
					return nil
				}
				raced = true
				if kind == "unrelated" {
					write(t, filepath.Join(f.writer, "README.md"), "Concurrent unrelated update\n")
				} else {
					category, tags := "concept", []string{"go"}
					if kind == "generated-taxonomy" {
						category, tags = "other", []string{"other-tag"}
					}
					f.addNote(t, 269, "another-writer", category, tags)
				}
				command(t, f.writer, "git", "add", ".")
				command(t, f.writer, "git", "commit", "-m", "Other writer")
				command(t, f.writer, "git", "push", "origin", "main")
				return nil
			}
			if err := f.repo.Publish(context.Background(), &job, checkpoint); err != nil {
				t.Fatal(err)
			}
			want := int64(270)
			if kind == "unrelated" {
				want = 269
			}
			if !raced || job.Number != want {
				t.Fatalf("number %d, want %d", job.Number, want)
			}
			command(t, f.writer, "git", "pull", "--ff-only")
			command(t, f.writer, "npm", "run", "taxonomy:sync")
			if command(t, f.writer, "git", "status", "--porcelain") != "" {
				t.Fatal("pushed taxonomy is stale")
			}
			if command(t, f.remote, "git", "rev-list", "--count", "main") != "3" {
				t.Fatal("race added or discarded commits")
			}
		})
	}
}

func TestLostPushCheckpointIsRecovered(t *testing.T) {
	f := newFixture(t)
	job := newJob()
	err := f.repo.Publish(context.Background(), &job, func() error {
		if job.State == "pushed" {
			return errors.New("simulated checkpoint failure after push")
		}
		return nil
	})
	if err == nil {
		t.Fatal("checkpoint failure ignored")
	}
	before := command(t, f.remote, "git", "rev-parse", "main")
	job.State = "queued"
	if err := f.repo.Publish(context.Background(), &job, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	if command(t, f.remote, "git", "rev-parse", "main") != before {
		t.Fatal("lost response caused a second commit")
	}
}

func TestCacheLockAndCredentialIsolation(t *testing.T) {
	f := newFixture(t)
	if other, err := open(context.Background(), Config{URL: f.remote, CacheDir: f.cache}); err == nil {
		other.Close()
		t.Fatal("second cache owner allowed")
	}
	t.Setenv("BOT_TOKEN", "bot-secret")
	t.Setenv("PORTFOLIO_GIT_TOKEN", "portfolio-secret")
	t.Setenv("CLAUDE_CODE_NOTIFY_APIKEY", "notify-secret")
	env := strings.Join(f.repo.environment(false), "\n")
	for _, secret := range []string{"bot-secret", "portfolio-secret", "notify-secret", "test-token-never-persist"} {
		if strings.Contains(env, secret) {
			t.Fatal("npm inherited a secret")
		}
	}
	for _, url := range []string{"git@github.com:someone/repo.git", "https://token@github.com/a/b.git", "https://other.example/a/b.git"} {
		if repo, err := Open(context.Background(), Config{URL: url, CacheDir: t.TempDir(), Token: "secret"}); err == nil {
			repo.Close()
			t.Fatal("unsafe transport accepted")
		}
	}
}

func TestRestartAbortsInterruptedRebaseInOwnedCache(t *testing.T) {
	f := newFixture(t)
	write(t, filepath.Join(f.cache, "README.md"), "Local candidate\n")
	command(t, f.cache, "git", "add", "README.md")
	command(t, f.cache, "git", "commit", "-m", "Local candidate")
	write(t, filepath.Join(f.writer, "README.md"), "Concurrent remote update\n")
	command(t, f.writer, "git", "add", "README.md")
	command(t, f.writer, "git", "commit", "-m", "Concurrent update")
	command(t, f.writer, "git", "push", "origin", "main")
	command(t, f.cache, "git", "fetch", "origin", "main")
	cmd := exec.Command("git", "rebase", "origin/main")
	cmd.Dir = f.cache
	if data, err := cmd.CombinedOutput(); err == nil {
		t.Fatalf("expected conflicting rebase, got %s", data)
	}
	if _, err := os.Stat(filepath.Join(f.cache, ".git/rebase-merge")); err != nil {
		t.Fatal("no interrupted rebase state")
	}
	f.repo.Close()
	f.repo = nil
	repo, err := open(context.Background(), Config{URL: f.remote, CacheDir: f.cache})
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	if command(t, f.cache, "git", "symbolic-ref", "--short", "HEAD") != "main" {
		t.Fatal("restart left detached HEAD")
	}
	if _, err := os.Stat(filepath.Join(f.cache, ".git/rebase-merge")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("restart left rebase state")
	}
}

func TestWriterAdvancesBetweenRebaseAndPush(t *testing.T) {
	f := newFixture(t)
	job := newJob()
	commits := 0
	checkpoint := func() error {
		if job.CommitSHA == "" || job.State == "pushed" {
			return nil
		}
		commits++
		// Both after the first commit and immediately before its push.
		if commits <= 2 {
			write(t, filepath.Join(f.writer, "README.md"), fmt.Sprintf("Concurrent update %d\n", commits))
			command(t, f.writer, "git", "add", "README.md")
			command(t, f.writer, "git", "commit", "-m", fmt.Sprintf("Concurrent update %d", commits))
			command(t, f.writer, "git", "push", "origin", "main")
		}
		return nil
	}
	if err := f.repo.Publish(context.Background(), &job, checkpoint); err != nil {
		t.Fatal(err)
	}
	if job.Number != 269 || command(t, f.remote, "git", "rev-list", "--count", "main") != "4" {
		t.Fatal("push race discarded a commit or duplicated publication")
	}
}
