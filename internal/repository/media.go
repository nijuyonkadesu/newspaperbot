package repository

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"slices"

	"newspaperbot/internal/media"
	"newspaperbot/internal/post"
)

// Images are fetched only while preparing a publication. The draft contains
// Telegram IDs; image bytes live in the checkout and the resulting Git commit.
func (r *Repository) prepareImages(ctx context.Context, d post.Draft) ([]string, error) {
	files := d.ImageFiles()
	if len(files) == 0 {
		return nil, nil
	}
	root, err := os.OpenRoot(r.config.CacheDir)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	if err := root.MkdirAll(post.ImageAssetDir, 0700); err != nil {
		return nil, err
	}
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	slices.Sort(names)
	var paths []string
	for _, name := range names {
		if !post.ValidImageAsset(name) {
			return nil, errors.New("invalid image asset path")
		}
		path := post.ImageAssetDir + name
		old, readErr := readImage(root, path)
		tracked, err := r.git(ctx, "ls-files", "--", path)
		if err != nil {
			return nil, err
		}
		if tracked != "" && readErr == nil {
			if err := media.ValidateImage(old, name); err != nil {
				return nil, err
			}
			paths = append(paths, path)
			continue
		}
		if r.config.DownloadMedia == nil {
			return nil, errors.New("image retrieval is not configured")
		}
		data, err := r.config.DownloadMedia(ctx, files[name])
		if err != nil {
			return nil, &media.ImageError{FileID: files[name], Reason: err.Error()}
		}
		if err := media.ValidateImage(data, name); err != nil {
			return nil, &media.ImageError{FileID: files[name], Reason: err.Error()}
		}
		if readErr == nil && !bytes.Equal(old, data) {
			return nil, errors.New("image asset conflicts with an existing file")
		}
		if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
			return nil, readErr
		}
		if err := root.WriteFile(path, data, 0600); err != nil {
			return nil, err
		}
		paths = append(paths, path)
	}
	return paths, nil
}

func readImage(root *os.Root, path string) ([]byte, error) {
	file, err := root.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, media.MaxImageBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > media.MaxImageBytes {
		return nil, errors.New("existing image asset exceeds 5 MiB")
	}
	return data, nil
}
