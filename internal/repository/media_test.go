package repository

import (
	"bytes"
	"context"
	"image"
	"image/jpeg"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"newspaperbot/internal/post"
)

func testPhoto(t *testing.T) []byte {
	t.Helper()
	var data bytes.Buffer
	if err := jpeg.Encode(&data, image.NewRGBA(image.Rect(0, 0, 2, 2)), nil); err != nil {
		t.Fatal(err)
	}
	return data.Bytes()
}

func TestPublishCommitsPhotoWithMarkdownAndRecoveryDoesNotRetrieveAgain(t *testing.T) {
	f := newFixture(t)
	photo := testPhoto(t)
	job := newJob()
	job.Draft.Category, job.Draft.Tags = "new category", []string{"new-tag"}
	name := post.ImageAsset("unique-original", "jpg")
	if err := job.Draft.Append(post.Source{MessageID: 42, Media: &post.Media{Kind: "photo", FileID: "private-photo-id", Asset: name}, Text: "Caption"}); err != nil {
		t.Fatal(err)
	}
	fetched := 0
	f.repo.config.DownloadMedia = func(ctx context.Context, id string) ([]byte, error) {
		fetched++
		if id != "private-photo-id" {
			t.Fatal("wrong Telegram size/reference")
		}
		return photo, nil
	}
	if err := f.repo.Publish(context.Background(), &job, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	if fetched != 1 {
		t.Fatal("retrieved the image more than once per publish", fetched)
	}
	changed := command(t, f.remote, "git", "diff-tree", "--no-commit-id", "--name-only", "-r", job.CommitSHA)
	for _, path := range []string{job.Filename, "category.yaml", "tag.yaml", post.ImageAssetDir + name} {
		if !strings.Contains(changed, path) {
			t.Fatal("image and article did not land in one commit", changed)
		}
	}
	committed := command(t, f.remote, "git", "show", job.CommitSHA+":"+job.Filename)
	if !strings.Contains(committed, "![]("+post.ImageURLDir+name+")\n\nCaption") || strings.Contains(committed, "private-photo-id") || strings.Contains(committed, "tg://") {
		t.Fatal("Markdown lost the image/caption or leaked Telegram-only data")
	}
	data, err := os.ReadFile(filepath.Join(f.cache, post.ImageAssetDir, name))
	if err != nil || !bytes.Equal(data, photo) {
		t.Fatal("repository image differs from downloaded source", err)
	}
	head := command(t, f.remote, "git", "rev-parse", "main")
	if err := f.repo.Publish(context.Background(), &job, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	if fetched != 1 || command(t, f.remote, "git", "rev-parse", "main") != head {
		t.Fatal("retry retrieved or committed a confirmed publication again")
	}
}

func TestInvalidImageNeverCommitsOrOverwritesOutsideCheckout(t *testing.T) {
	f := newFixture(t)
	job := newJob()
	name := post.ImageAsset("bad", "jpg")
	if err := job.Draft.Append(post.Source{MessageID: 42, Media: &post.Media{Kind: "photo", FileID: "photo", Asset: name}}); err != nil {
		t.Fatal(err)
	}
	head := command(t, f.remote, "git", "rev-parse", "main")
	f.repo.config.DownloadMedia = func(context.Context, string) ([]byte, error) { return []byte("not an image"), nil }
	if err := f.repo.Publish(context.Background(), &job, func() error { return nil }); err == nil {
		t.Fatal("invalid image was accepted")
	}
	if job.CommitSHA != "" || command(t, f.remote, "git", "rev-parse", "main") != head {
		t.Fatal("invalid image produced a commit")
	}
	outside := t.TempDir()
	if err := os.MkdirAll(filepath.Join(f.cache, "src"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(f.cache, "src", "assets")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(f.cache, "src", "assets")); err != nil {
		t.Fatal(err)
	}
	f.repo.config.DownloadMedia = func(context.Context, string) ([]byte, error) { return testPhoto(t), nil }
	if err := f.repo.Publish(context.Background(), &job, func() error { return nil }); err == nil {
		t.Fatal("asset symlink escaped the checkout")
	}
	entries, err := os.ReadDir(outside)
	if err != nil || len(entries) != 0 {
		t.Fatal("publication wrote outside its checkout", err)
	}
}
