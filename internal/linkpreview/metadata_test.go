package linkpreview

import (
	"bytes"
	"image"
	"image/gif"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestRedditUsesPostMetadataInsteadOfSiteShell(t *testing.T) {
	data, err := os.ReadFile("testdata/reddit-post.json")
	if err != nil {
		t.Fatal(err)
	}
	const raw = "https://www.reddit.com/r/vim/comments/mrpoa3/git_workflow_with_understanding_file_history/"
	client := New(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		body := []byte("<title>Reddit</title>")
		if r.URL.Path == "/oembed" {
			if r.URL.Query().Get("url") != raw {
				t.Fatal("Reddit oEmbed lost the original post", r.URL)
			}
			body = data
		}
		return &http.Response{StatusCode: 200, Header: http.Header{}, Request: r, Body: io.NopCloser(bytes.NewReader(body))}, nil
	})})
	card := client.Lookup(t.Context(), raw)
	if card.URL != raw || card.Title != "Git workflow with understanding file history (fugitive.vim, gv.vim, vim-flog, .. others?)" || card.Description != "r/vim · u/miscjunk" {
		t.Fatal("Reddit post was replaced with a useless brand-only card", card.Title, card.Description)
	}
}

func TestSlowAvailablePageStillHasPreview(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(2100 * time.Millisecond)
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte("<title>Slow but useful page</title>"))
	}))
	defer server.Close()
	if card := New(server.Client()).Lookup(t.Context(), server.URL); card.Title != "Slow but useful page" {
		t.Fatal("healthy page lost to an overly short deadline", card.Title)
	}
}

func TestOEmbedCannotReplaceUsefulMetadataOrReachPrivateServices(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("oEmbed reached a host service")
	}))
	defer server.Close()
	for _, hasOGTitle := range []bool{false, true} {
		client := New(nil)
		guarded := client.http.Transport
		requests := 0
		client.http.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
			if r.URL.Hostname() == "public.test" {
				requests++
				body := `<title>Original title</title><meta name=description content="Original description"><link rel=alternate type="application/json+oembed" href="` + server.URL + `">`
				if hasOGTitle {
					body += `<meta property=og:title content="Original title">`
				}
				return &http.Response{StatusCode: 200, Header: http.Header{}, Request: r, Body: io.NopCloser(strings.NewReader(body))}, nil
			}
			return guarded.RoundTrip(r)
		})
		card := client.Lookup(t.Context(), "https://public.test/post")
		if card.Title != "Original title" || card.Description != "Original description" || requests != 1 {
			t.Fatal("oEmbed failure discarded usable page metadata", card.Title, card.Description)
		}
	}
}

func TestXPlaceholderUsesEmbeddedPostText(t *testing.T) {
	data, err := os.ReadFile("testdata/x-post.json")
	if err != nil {
		t.Fatal(err)
	}
	client := New(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		body := []byte(`<title>X on X</title><meta property=og:description content="X on X">`)
		if r.URL.Hostname() == "publish.twitter.com" {
			body = data
		}
		return &http.Response{StatusCode: 200, Header: http.Header{}, Request: r, Body: io.NopCloser(bytes.NewReader(body))}, nil
	})})
	card := client.Lookup(t.Context(), "https://twitter.com/RKBDI/status/1744146767724130455")
	if card.Title != "RKBDI" || card.Description != "for fonts i recommend using https://t.co/yWziwlv8JY" {
		t.Fatal("X embed discarded the post text", card.Title, card.Description)
	}
}

func TestBlockedXPageCanStillUseOEmbed(t *testing.T) {
	data, err := os.ReadFile("testdata/x-post.json")
	if err != nil {
		t.Fatal(err)
	}
	client := New(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		status, body := http.StatusForbidden, []byte("Blocked")
		if r.URL.Hostname() == "publish.twitter.com" {
			status, body = http.StatusOK, data
		}
		return &http.Response{StatusCode: status, Header: http.Header{}, Request: r, Body: io.NopCloser(bytes.NewReader(body))}, nil
	})})
	if card := client.Lookup(t.Context(), "https://x.com/RKBDI/status/1744146767724130455"); card.Title != "RKBDI" || card.Description == "" {
		t.Fatal("page blocking prevented the working embed endpoint", card.Title, card.Description)
	}
}

func TestYouTubePlaylistMetadataAfterBodyIsCaptured(t *testing.T) {
	page := "<head><title>YouTube</title><script>" + strings.Repeat(" ", 700<<10) + "</script></head><body>" +
		`<meta property=og:title content="Design Patterns in Object Oriented Programming"><meta property=og:description content="Video series on Design Patterns."><main>Content</main>`
	client := New(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/html"}}, Request: r,
			Body: io.NopCloser(strings.NewReader(page))}, nil
	})})
	card := client.Lookup(t.Context(), "https://www.youtube.com/playlist?list=example")
	if card.Title != "Design Patterns in Object Oriented Programming" || card.Description != "Video series on Design Patterns." {
		t.Fatal("late playlist metadata discarded", card.Title, card.Description)
	}
}

func TestUselessSiteAndChallengeCardsAreOmitted(t *testing.T) {
	for _, title := range []string{"example", "Access denied", "Just a moment...", "Attention Required! | Cloudflare", "Please Wait..."} {
		client := New(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Header: http.Header{}, Request: r,
				Body: io.NopCloser(strings.NewReader("<title>" + title + "</title>"))}, nil
		})})
		if card := client.Lookup(t.Context(), "https://example.com/post"); card.Title != "" {
			t.Fatal("site/challenge label became preview noise", card.Title)
		}
	}
}

func TestGIFAndWebPThumbnailsBecomeSupportedPhotos(t *testing.T) {
	var gifImage bytes.Buffer
	if err := gif.Encode(&gifImage, image.NewRGBA(image.Rect(0, 0, 2, 2)), nil); err != nil {
		t.Fatal(err)
	}
	webp, err := os.ReadFile("testdata/thumbnail.webp")
	if err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string][]byte{"gif": gifImage.Bytes(), "webp": webp} {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/photo" {
					w.Write(data)
					return
				}
				w.Header().Set("Content-Type", "text/html")
				w.Write([]byte(`<title>Real page title</title><meta property=og:image content="/photo">`))
			}))
			defer server.Close()
			card := New(server.Client()).Lookup(t.Context(), server.URL)
			_, format, err := image.DecodeConfig(bytes.NewReader(card.Image))
			if err != nil || format != card.ImageFormat || format != "png" && format != "jpeg" {
				t.Fatal("valid thumbnail was silently dropped or uploaded in an unsupported format", format, err)
			}
			original, _, err := image.Decode(bytes.NewReader(data))
			if err != nil {
				t.Fatal(err)
			}
			converted, _, err := image.Decode(bytes.NewReader(card.Image))
			if err != nil || converted.Bounds() != original.Bounds() {
				t.Fatal("thumbnail dimensions changed", err)
			}
			_, _, _, alpha := original.At(0, 0).RGBA()
			_, _, _, convertedAlpha := converted.At(0, 0).RGBA()
			if alpha != convertedAlpha {
				t.Fatal("thumbnail conversion lost transparency")
			}
		})
	}
}

func TestLargeGIFFirstFrameFitsTheDownloadLimit(t *testing.T) {
	var encoded bytes.Buffer
	if err := gif.Encode(&encoded, image.NewRGBA(image.Rect(0, 0, 2, 2)), nil); err != nil {
		t.Fatal(err)
	}
	// A valid large comment after the first frame represents the unnecessary
	// trailing bytes of an animation; Decode must not need to read them all.
	data := append([]byte{}, encoded.Bytes()[:encoded.Len()-1]...)
	data = append(data, 0x21, 0xfe)
	for len(data) <= maxImage {
		data = append(data, 255)
		data = append(data, bytes.Repeat([]byte{'x'}, 255)...)
	}
	data = append(data, 0, 0x3b)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/photo" {
			w.Write(data)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<title>Real page title</title><meta property=og:image content="/photo">`))
	}))
	defer server.Close()
	card := New(server.Client()).Lookup(t.Context(), server.URL)
	if len(card.Image) == 0 || len(card.Image) > maxImage {
		t.Fatal("bounded GIF first frame was lost", len(card.Image))
	}
}
