// Package repository owns the bot's disposable checkout of the portfolio.
// Git is the transport; the portfolio's npm command remains the taxonomy authority.
package repository

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"go.yaml.in/yaml/v3"
	"tgblogbot/internal/metadata"
	"tgblogbot/internal/post"
)

type Config struct{ URL, CacheDir, Token string }

type Repository struct {
	config    Config
	mu        sync.Mutex // One mutation/fetch at a time; never hold the catalog lock during I/O.
	catalogMu sync.RWMutex
	catalog   metadata.Catalog
	lock      *os.File
}

func Open(ctx context.Context, c Config) (*Repository, error) {
	u, err := url.Parse(c.URL)
	if err != nil || u.Scheme != "https" || u.Host != "github.com" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, errors.New("PORTFOLIO_REPO_URL must be a credential-free https://github.com repository URL")
	}
	if c.Token == "" {
		return nil, errors.New("PORTFOLIO_GIT_TOKEN is required")
	}
	return open(ctx, c)
}

func open(ctx context.Context, c Config) (*Repository, error) {
	dir, err := filepath.Abs(c.CacheDir)
	if err != nil {
		return nil, err
	}
	c.CacheDir = dir
	if err := os.MkdirAll(filepath.Dir(dir), 0700); err != nil {
		return nil, err
	}
	lock, err := os.OpenFile(dir+".lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		lock.Close()
		return nil, errors.New("another process is using the portfolio cache")
	}
	r := &Repository{config: c, lock: lock}
	ok := false
	defer func() {
		if !ok {
			r.Close()
		}
	}()
	if _, err := os.Stat(dir); errors.Is(err, os.ErrNotExist) {
		if _, err := r.git(ctx, "clone", "--single-branch", "--branch", "main", "--", c.URL, dir); err != nil {
			return nil, err
		}
	} else if err != nil {
		return nil, err
	}
	remote, err := r.git(ctx, "remote", "get-url", "origin")
	if err != nil || strings.TrimSpace(remote) != c.URL {
		return nil, errors.New("portfolio cache remote does not match configuration")
	}
	marker := filepath.Join(dir, ".git", "blogbot-cache")
	if data, err := os.ReadFile(marker); err == nil {
		if string(data) != c.URL {
			return nil, errors.New("portfolio cache ownership marker does not match")
		}
	} else if errors.Is(err, os.ErrNotExist) {
		status, err := r.git(ctx, "status", "--porcelain")
		if err != nil {
			return nil, err
		}
		if strings.TrimSpace(status) != "" {
			return nil, errors.New("refusing to adopt a dirty portfolio checkout")
		}
		if err := os.WriteFile(marker, []byte(c.URL), 0600); err != nil {
			return nil, err
		}
	} else {
		return nil, err
	}
	// A process crash during rebase can leave HEAD detached. Abort only in
	// this owned cache; the durable queue rebuilds the unpublished commit.
	for _, state := range []string{"rebase-merge", "rebase-apply"} {
		if _, err := os.Stat(filepath.Join(dir, ".git", state)); err == nil {
			if _, err := r.git(ctx, "rebase", "--abort"); err != nil {
				return nil, err
			}
			break
		}
	}
	branch, err := r.git(ctx, "symbolic-ref", "--short", "HEAD")
	if err != nil || strings.TrimSpace(branch) != "main" {
		return nil, errors.New("portfolio cache must use main")
	}
	if err := r.Refresh(ctx); err != nil {
		// An existing checkout can still supply a coherent last-known snapshot.
		if cachedErr := r.readCatalog(ctx); cachedErr != nil {
			return nil, err
		}
	}
	ok = true
	return r, nil
}

func (r *Repository) Close() error { return r.lock.Close() }

func (r *Repository) Catalog(context.Context) (metadata.Catalog, error) {
	r.catalogMu.RLock()
	defer r.catalogMu.RUnlock()
	if r.catalog.Revision == "" {
		return metadata.Catalog{}, errors.New("portfolio catalog is not available")
	}
	c := r.catalog
	c.Categories, c.Tags = slices.Clone(c.Categories), slices.Clone(c.Tags)
	c.Groups = make(map[string][]string, len(r.catalog.Groups))
	for category, tags := range r.catalog.Groups {
		c.Groups[category] = slices.Clone(tags)
	}
	return c, nil
}

func (r *Repository) Refresh(ctx context.Context) error {
	if !r.mu.TryLock() {
		return nil
	} // Publishing already fetches; authoring uses the last valid snapshot.
	defer r.mu.Unlock()
	if err := r.fetch(ctx); err != nil {
		return err
	}
	return r.readCatalog(ctx)
}

func (r *Repository) fetch(ctx context.Context) error {
	_, err := r.git(ctx, "fetch", "--no-tags", "origin", "main")
	return err
}

func (r *Repository) readCatalog(ctx context.Context) error {
	var c metadata.Catalog
	revision, err := r.git(ctx, "rev-parse", "origin/main")
	if err != nil {
		return err
	}
	c.Revision = strings.TrimSpace(revision)
	cats, err := r.git(ctx, "show", "origin/main:category.yaml")
	if err == nil {
		if err := yaml.Unmarshal([]byte(cats), &c.Categories); err != nil {
			return fmt.Errorf("category.yaml: %w", err)
		}
	} else {
		cats, err = r.git(ctx, "show", "origin/main:category.txt")
		if err != nil {
			return errors.New("portfolio must contain category.yaml or category.txt")
		}
		c.Categories = strings.Split(strings.TrimSpace(cats), "\n")
	}
	tags, err := r.git(ctx, "show", "origin/main:tag.yaml")
	if err != nil {
		tags, err = r.git(ctx, "show", "origin/main:tag.txt")
	}
	if err != nil {
		return errors.New("portfolio must contain tag.yaml or tag.txt")
	}
	if err := yaml.Unmarshal([]byte(tags), &c.Groups); err != nil {
		return fmt.Errorf("category-to-tags mapping: %w", err)
	}
	if len(c.Categories) == 0 {
		return errors.New("portfolio categories are empty")
	}
	for i, category := range c.Categories {
		if err := post.ValidateCategory(category); err != nil {
			return err
		}
		if slices.Contains(c.Categories[:i], category) {
			return errors.New("portfolio contains duplicate categories")
		}
		if _, ok := c.Groups[category]; !ok {
			return fmt.Errorf("tag mapping is missing category %q", category)
		}
	}
	for category, tags := range c.Groups {
		if !slices.Contains(c.Categories, category) {
			return fmt.Errorf("tag mapping has unknown category %q", category)
		}
		for i, tag := range tags {
			if !post.ValidSlug(tag) || slices.Contains(tags[:i], tag) {
				return fmt.Errorf("invalid or duplicate tag %q in %q", tag, category)
			}
			if !slices.Contains(c.Tags, tag) {
				c.Tags = append(c.Tags, tag)
			}
		}
	}
	slices.Sort(c.Tags)
	files, err := r.git(ctx, "ls-tree", "-r", "--name-only", "origin/main", "--", "content/tweets")
	if err != nil {
		return err
	}
	for _, file := range strings.Split(files, "\n") {
		prefix, _, found := strings.Cut(filepath.Base(file), "-")
		if !found || !strings.HasSuffix(file, ".md") {
			continue
		}
		n, err := strconv.ParseInt(prefix, 10, 64)
		if err != nil || n < 1 {
			return fmt.Errorf("invalid portfolio note filename %q", file)
		}
		c.LastNumber = max(c.LastNumber, n)
	}
	r.catalogMu.Lock()
	r.catalog = c
	r.catalogMu.Unlock()
	return nil
}

// Child processes never inherit the bot's secrets. Git receives its PAT only
// through a scoped ephemeral config environment, never argv or .git/config.
func (r *Repository) environment(git bool) []string {
	var env []string
	for _, pair := range os.Environ() {
		key, _, _ := strings.Cut(pair, "=")
		switch key {
		case "PATH", "HOME", "TMPDIR", "LANG", "LC_ALL", "TZ", "SSL_CERT_FILE", "SSL_CERT_DIR":
			env = append(env, pair)
		}
	}
	if git {
		env = append(env, "GIT_TERMINAL_PROMPT=0", "GIT_CONFIG_COUNT=3", "GIT_CONFIG_KEY_0=credential.helper", "GIT_CONFIG_VALUE_0=", "GIT_CONFIG_KEY_1=http.https://github.com/.extraheader", "GIT_CONFIG_VALUE_1=Authorization: Basic "+base64.StdEncoding.EncodeToString([]byte("x-access-token:"+r.config.Token)), "GIT_CONFIG_KEY_2=core.hooksPath", "GIT_CONFIG_VALUE_2=/dev/null", "GIT_EDITOR=true")
	}
	return env
}

func (r *Repository) command(ctx context.Context, name string, args ...string) (string, error) {
	limit := 45 * time.Second
	if name == "npm" {
		limit = 3 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil {
			if errors.Is(err, syscall.ESRCH) {
				return os.ErrProcessDone
			}
			return err
		}
		return nil
	}
	cmd.WaitDelay = 5 * time.Second
	if _, err := os.Stat(r.config.CacheDir); err == nil {
		cmd.Dir = r.config.CacheDir
	}
	cmd.Env = r.environment(name == "git")
	output, err := cmd.CombinedOutput()
	if err != nil {
		verb := args[0]
		if name == "git" {
			for i := 0; i < len(args); i++ {
				if args[i] == "-c" {
					i++
					continue
				}
				verb = args[i]
				break
			}
		}
		// No remote response, command output, or headers enter logs/errors.
		if ctx.Err() != nil {
			return "", fmt.Errorf("%s %s: %w", name, verb, ctx.Err())
		}
		return "", fmt.Errorf("%s %s failed (%v)", name, verb, err)
	}
	return string(output), nil
}

func (r *Repository) git(ctx context.Context, args ...string) (string, error) {
	return r.command(ctx, "git", append([]string{"-c", "user.name=Newspaper bot", "-c", "user.email=newspaperbot@users.noreply.github.com", "-c", "commit.gpgSign=false", "-c", "rebase.autoStash=false"}, args...)...)
}

func (r *Repository) install(ctx context.Context) error {
	data, err := os.ReadFile(filepath.Join(r.config.CacheDir, "package-lock.json"))
	if err != nil {
		return errors.New("portfolio requires package-lock.json")
	}
	sum := sha256.Sum256(data)
	hash := hex.EncodeToString(sum[:])
	marker := filepath.Join(r.config.CacheDir, ".git", "blogbot-npm-lock")
	old, _ := os.ReadFile(marker)
	_, depsErr := os.Stat(filepath.Join(r.config.CacheDir, "node_modules"))
	if string(old) == hash && depsErr == nil {
		return nil
	}
	if _, err := r.command(ctx, "npm", "ci", "--ignore-scripts", "--no-audit", "--no-fund"); err != nil {
		return err
	}
	return os.WriteFile(marker, []byte(hash), 0600)
}
