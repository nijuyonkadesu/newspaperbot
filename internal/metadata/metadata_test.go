package metadata

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type testTransport struct{ handler http.Handler }

func (t testTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	w := httptest.NewRecorder()
	t.handler.ServeHTTP(w, r)
	return w.Result(), nil
}

func TestLocalFixtures(t *testing.T) {
	l := Loader{NumberSource: "../../testdata/blog-number.json", CategoriesSource: "../../testdata/categories.json", TagsSource: "../../testdata/tags.json"}
	c, err := l.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if c.LastNumber != 42 || len(c.Categories) != 2 || len(c.Tags) != 3 {
		t.Fatalf("unexpected fixtures: %+v", c)
	}
}

func TestHTTPMetadataAndFailures(t *testing.T) {
	for _, tt := range []struct {
		name, number, categories, tags string
		status                         int
		wantError                      bool
	}{
		{"valid", `{"last_post_number":42}`, `["development"]`, `["go"]`, 200, false},
		{"missing number", `{}`, `["development"]`, `[]`, 200, true},
		{"negative number", `{"last_post_number":-1}`, `["development"]`, `[]`, 200, true},
		{"invalid JSON", `{"last_post_number":42}`, `broken`, `[]`, 200, true},
		{"empty categories", `{"last_post_number":42}`, `[]`, `[]`, 200, true},
		{"duplicate tags", `{"last_post_number":42}`, `["development"]`, `["go","go"]`, 200, true},
		{"comma in tag", `{"last_post_number":42}`, `["development"]`, `["go,sqlite"]`, 200, true},
		{"server failure", `{"last_post_number":42}`, `["development"]`, `[]`, 503, true},
		{"oversized", strings.Repeat(" ", (1<<20)+1), `["development"]`, `[]`, 200, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			transport := testTransport{handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.status)
				switch r.URL.Path {
				case "/number":
					w.Write([]byte(tt.number))
				case "/categories":
					w.Write([]byte(tt.categories))
				case "/tags":
					w.Write([]byte(tt.tags))
				}
			})}
			l := Loader{NumberSource: "https://metadata.test/number", CategoriesSource: "https://metadata.test/categories", TagsSource: "https://metadata.test/tags", Client: &http.Client{Transport: transport}}
			_, err := l.Load(context.Background())
			if (err != nil) != tt.wantError {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestCancelledMetadataRequest(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	l := Loader{NumberSource: "https://example.invalid/number"}
	if _, err := l.Load(ctx); err == nil {
		t.Fatal("cancelled request succeeded")
	}
}
