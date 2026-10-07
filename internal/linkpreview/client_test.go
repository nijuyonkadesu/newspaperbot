package linkpreview

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestFirstURL(t *testing.T) {
	for _, tc := range []struct{ text, want string }{
		{"Read https://example.com/a.", "https://example.com/a"},
		{"[Guide](https://example.com/a?x=1&y=2)", "https://example.com/a?x=1&y=2"},
		{"[Wiki](https://example.com/A_(B))", "https://example.com/A_(B)"},
		{"[Editor](https://example.com/)Next", "https://example.com/"},
		{"(https://example.com/post)#tag", "https://example.com/post"},
		{"<https://example.com/a>", "https://example.com/a"},
		{"https://[2001:4860:4860::8888]", "https://[2001:4860:4860::8888]"},
		{"`https://code.test` https://example.com", "https://example.com"},
		{"``https://code.test ` tick`` https://example.com", "https://example.com"},
		{"```go\nhttps://code.test\n```\nhttps://example.com", "https://example.com"},
		{"````md\n```\nhttps://code.test\n````\nhttps://example.com", "https://example.com"},
		{"    ```\nhttps://example.com", "https://example.com"},
		{"~~~\nhttps://code.test\n~~~", ""},
		{"    https://code.test\nhttps://example.com", "https://example.com"},
		{"https://user:password@example.com", ""},
		{"tg://user?id=42\njavascript:alert(1)", ""},
	} {
		t.Run(tc.text, func(t *testing.T) {
			if got := firstURL(tc.text); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestMetadataAndImageAreCached(t *testing.T) {
	var requests atomic.Int32
	var photo bytes.Buffer
	if err := png.Encode(&photo, image.NewRGBA(image.Rect(0, 0, 24, 16))); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		switch r.URL.Path {
		case "/start":
			http.Redirect(w, r, "/article/page", http.StatusFound)
		case "/article/page":
			w.Header().Set("Content-Type", "text/html; charset=UTF-8")
			w.Write([]byte(`<html><head><title>Fallback title</title>
<meta content="A &amp; B" property=og:title>
<meta name=twitter:description content="  A useful
description.  "><meta property=og:image content="../image.png"></head></html>`))
		case "/image.png":
			w.Header().Set("Content-Type", "image/png")
			w.Write(photo.Bytes())
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client := New(server.Client())
	raw := server.URL + "/start"
	card := client.Lookup(t.Context(), "[article]("+raw+")")
	if card.URL != raw || card.Title != "A & B" || card.Description != "A useful description." || card.ImageFormat != "png" || !bytes.Equal(card.Image, photo.Bytes()) {
		t.Fatalf("metadata or relative image lost: %+v", card)
	}
	client.Lookup(t.Context(), raw)
	if requests.Load() != 3 {
		t.Fatal("cached card refetched its page or thumbnail")
	}
	client.OmitImage(raw)
	if card := client.Lookup(t.Context(), raw); card.Title != "A & B" || len(card.Image) != 0 || requests.Load() != 3 {
		t.Fatal("image rejection lost the text card or refetched the image")
	}
}

func TestLargePageWithSmallHeadStillHasPreview(t *testing.T) {
	const head = `<head><meta property="og:title" content="Usable title"></head><body>`
	body := strings.NewReader(head + strings.Repeat("x", 2*maxHTML))
	client := New(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/html"}},
			Body: io.NopCloser(body), Request: r}, nil
	})})
	if card := client.Lookup(t.Context(), "https://example.com/article"); card.Title != "Usable title" {
		t.Fatal("large body discarded metadata already available in the head", card.Title)
	}
	if consumed := len(head) + 2*maxHTML - body.Len(); consumed > 16<<10 {
		t.Fatalf("metadata lookup read the article body: %d bytes", consumed)
	}
}

func TestYouTubePreviewUsesSmallOEmbedResponse(t *testing.T) {
	var photo bytes.Buffer
	if err := png.Encode(&photo, image.NewRGBA(image.Rect(0, 0, 24, 16))); err != nil {
		t.Fatal(err)
	}
	requests := 0
	client := New(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		requests++
		var body io.Reader
		switch r.URL.Hostname() {
		case "www.youtube.com":
			if r.URL.Path != "/oembed" || r.URL.Query().Get("format") != "json" || r.URL.Query().Get("url") != "https://www.youtube.com/watch?v=XO-7pyzmbLs" {
				t.Errorf("requested the heavy watch page instead of video metadata: %s", r.URL)
				return nil, io.ErrUnexpectedEOF
			}
			body = strings.NewReader(`{"title":"Train yourself Like a Ayanokoji","author_name":"Emo Analysis","thumbnail_url":"https://i.ytimg.com/vi/XO-7pyzmbLs/hqdefault.jpg","html":"<iframe>unused</iframe>"}`)
		case "i.ytimg.com":
			body = bytes.NewReader(photo.Bytes())
		default:
			t.Fatalf("unexpected video metadata request: %s", r.URL)
		}
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(body), Request: r}, nil
	})})
	raw := "https://www.youtube.com/watch?v=XO-7pyzmbLs"
	card := client.Lookup(t.Context(), raw)
	if card.URL != raw || card.Title != "Train yourself Like a Ayanokoji" || card.Description != "Emo Analysis" || !bytes.Equal(card.Image, photo.Bytes()) {
		t.Fatal("YouTube video metadata or thumbnail lost", card.Title, card.Description)
	}
	client.Lookup(t.Context(), raw)
	if requests != 2 {
		t.Fatal("YouTube metadata or thumbnail bypassed the cache", requests)
	}
}

func TestUnavailableOrUnpreviewablePagesAreCachedMisses(t *testing.T) {
	for _, tc := range []struct {
		name, contentType, body string
		status                  int
	}{
		{"no metadata", "text/html", "<html><body>Just text.</body></html>", 200},
		{"empty metadata", "text/html", `<meta property=og:title content="  ">`, 200},
		{"not HTML", "application/json", `{"title":"Not a page"}`, 200},
		{"not found", "text/html", "<title>Not found</title>", 404},
		{"unavailable", "text/html", "<title>Server error</title>", 503},
		{"too large", "text/html", "<title>Big</title>" + strings.Repeat("x", maxHTML), 200},
		{"invalid UTF-8", "text/html", "<title>Bad\xff</title>", 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				w.Header().Set("Content-Type", tc.contentType)
				w.WriteHeader(tc.status)
				w.Write([]byte(tc.body))
			}))
			defer server.Close()
			client := New(server.Client())
			for range 2 {
				if card := client.Lookup(t.Context(), server.URL); card.Title != "" || len(card.Image) != 0 {
					t.Fatalf("failed page produced a card: %+v", card)
				}
			}
			if requests.Load() != 1 {
				t.Fatal("failed URL retried on every render")
			}
		})
	}
}

func TestImageFailuresKeepTheTextCard(t *testing.T) {
	for _, tc := range []struct {
		name, image string
		status      int
	}{
		{"missing", "", 404},
		{"not an image", "<html>not a picture</html>", 200},
		{"too large", strings.Repeat("x", maxImage+1), 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/image" {
					w.WriteHeader(tc.status)
					w.Write([]byte(tc.image))
					return
				}
				w.Header().Set("Content-Type", "text/html")
				w.Write([]byte(`<meta property=og:title content="Good title"><meta property=og:image content="/image">`))
			}))
			defer server.Close()
			card := New(server.Client()).Lookup(t.Context(), server.URL)
			if card.Title != "Good title" || len(card.Image) != 0 {
				t.Fatal("bad thumbnail discarded the usable title", card)
			}
		})
	}
}

func TestTimeoutCancellationAndConnectionFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	client := New(&http.Client{Timeout: 30 * time.Millisecond})
	if card := client.Lookup(t.Context(), server.URL); card.Title != "" {
		t.Fatal("timed-out URL produced a card")
	}
	server.Close()
	if card := New(server.Client()).Lookup(t.Context(), server.URL); card.Title != "" {
		t.Fatal("unreachable URL produced a card")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if card := client.Lookup(ctx, server.URL); card.Title != "" {
		t.Fatal("canceled request used cached metadata")
	}
}

func TestRedirectLimitAndNegativeCacheRecovery(t *testing.T) {
	var loop atomic.Bool
	loop.Store(true)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if loop.Load() {
			http.Redirect(w, r, "/again", http.StatusFound)
		} else {
			w.Header().Set("Content-Type", "text/html")
			w.Write([]byte("<title>Recovered</title>"))
		}
	}))
	defer server.Close()
	client := New(server.Client())
	if card := client.Lookup(t.Context(), server.URL); card.Title != "" {
		t.Fatal("redirect loop was accepted")
	}
	loop.Store(false)
	client.mu.Lock()
	cached := client.cache[server.URL]
	cached.expires = time.Now().Add(-time.Second)
	client.cache[server.URL] = cached
	client.mu.Unlock()
	if card := client.Lookup(t.Context(), server.URL); card.Title != "Recovered" {
		t.Fatal("negative cache never recovered", card)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestPublicTransportCannotReachPrivateAddressesOrRedirects(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("preview reached a host service")
	}))
	defer server.Close()
	if card := New(nil).Lookup(t.Context(), server.URL); card.Title != "" {
		t.Fatal("private URL was allowed")
	}
	for _, address := range []string{"127.0.0.1:80", "10.1.2.3:80", "169.254.169.254:80", "100.100.100.200:80", "[::1]:80", "[::ffff:127.0.0.1]:80"} {
		if conn, err := publicDial(t.Context(), "tcp", address); err == nil {
			conn.Close()
			t.Fatal("private address accepted", address)
		}
	}
	client := New(nil)
	guarded := client.http.Transport
	client.http.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Hostname() == "public.test" {
			w := httptest.NewRecorder()
			w.Header().Set("Location", server.URL)
			w.WriteHeader(http.StatusFound)
			result := w.Result()
			result.Request = r
			return result, nil
		}
		return guarded.RoundTrip(r)
	})
	if card := client.Lookup(t.Context(), "https://public.test"); card.Title != "" {
		t.Fatal("redirect to private target was accepted")
	}
}

func TestCacheStaysBoundedAndIsSafeForConcurrentHits(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte("<title>Cached</title>"))
	}))
	defer server.Close()
	client := New(server.Client())
	for i := range maxCache + 5 {
		client.Lookup(t.Context(), server.URL+"?n="+strconv.Itoa(i))
	}
	if len(client.cache) > maxCache {
		t.Fatal("cache grows without a bound")
	}
	client.Lookup(t.Context(), server.URL)
	for range 10 {
		t.Run("hit", func(t *testing.T) {
			t.Parallel()
			if client.Lookup(t.Context(), server.URL).Title != "Cached" {
				t.Error("concurrent cache hit lost the card")
			}
		})
	}
}
