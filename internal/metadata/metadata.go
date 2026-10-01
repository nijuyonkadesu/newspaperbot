package metadata

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strings"
	"time"
)

type Catalog struct {
	LastNumber int64
	Categories []string
	Tags       []string
	Groups     map[string][]string
	Revision   string
}

type Loader struct {
	NumberSource, CategoriesSource, TagsSource string
	Client                                     *http.Client
}

func (l Loader) Load(ctx context.Context) (Catalog, error) {
	var number struct {
		LastPostNumber *int64 `json:"last_post_number"`
	}
	if err := l.read(ctx, l.NumberSource, &number); err != nil {
		return Catalog{}, fmt.Errorf("post number metadata: %w", err)
	}
	if number.LastPostNumber == nil || *number.LastPostNumber < 0 {
		return Catalog{}, errors.New("last_post_number must be a non-negative integer")
	}
	c, err := l.Taxonomy(ctx)
	c.LastNumber = *number.LastPostNumber
	return c, err
}

// Taxonomy can be read independently of the publication number source.
func (l Loader) Taxonomy(ctx context.Context) (Catalog, error) {
	var c Catalog
	for _, source := range []struct {
		path   string
		target any
	}{
		{l.CategoriesSource, &c.Categories}, {l.TagsSource, &c.Tags},
	} {
		if err := l.read(ctx, source.path, source.target); err != nil {
			return c, fmt.Errorf("taxonomy metadata: %w", err)
		}
	}
	if len(c.Categories) == 0 {
		return c, errors.New("at least one category is required")
	}
	for _, names := range [][]string{c.Categories, c.Tags} {
		for i, name := range names {
			if name == "" || name != strings.TrimSpace(name) || strings.ContainsAny(name, ",\r\n") || slices.Contains(names[:i], name) {
				return c, fmt.Errorf("invalid or duplicate category/tag %q", name)
			}
		}
	}
	return c, nil
}

func (l Loader) read(ctx context.Context, source string, target any) error {
	u, err := url.Parse(source)
	if err != nil {
		return err
	}
	var reader io.ReadCloser
	if u.Scheme == "http" || u.Scheme == "https" {
		client := l.Client
		if client == nil {
			client = &http.Client{Timeout: 10 * time.Second}
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, source, nil)
		if err != nil {
			return err
		}
		response, err := client.Do(req)
		if err != nil {
			return err
		}
		if response.StatusCode != http.StatusOK {
			response.Body.Close()
			return fmt.Errorf("HTTP status %d", response.StatusCode)
		}
		reader = response.Body
	} else {
		if u.Scheme != "" {
			return fmt.Errorf("unsupported scheme %q", u.Scheme)
		}
		reader, err = os.Open(source)
		if err != nil {
			return err
		}
	}
	defer reader.Close()
	const limit = 1 << 20
	data, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return err
	}
	if len(data) > limit {
		return errors.New("metadata exceeds 1 MiB")
	}
	return json.Unmarshal(data, target)
}
