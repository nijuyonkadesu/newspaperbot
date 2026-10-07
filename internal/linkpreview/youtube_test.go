package linkpreview

import (
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestYouTubeURLForms(t *testing.T) {
	for _, raw := range []string{
		"https://www.youtube.com/watch?v=XO-7pyzmbLs&list=playlist&t=20",
		"https://youtu.be/XO-7pyzmbLs?si=share",
		"https://m.youtube.com/watch?v=XO-7pyzmbLs",
		"https://music.youtube.com/watch?v=XO-7pyzmbLs",
		"https://youtube.com/shorts/XO-7pyzmbLs",
		"https://www.youtube.com/embed/XO-7pyzmbLs",
		"https://www.youtube.com/live/XO-7pyzmbLs",
		"https://www.youtube-nocookie.com/embed/XO-7pyzmbLs",
	} {
		u, _ := url.Parse(raw)
		endpoint, err := url.Parse(youtubeEndpoint(u))
		if err != nil || endpoint.Hostname() != "www.youtube.com" || endpoint.Path != "/oembed" || endpoint.Query().Get("url") != "https://www.youtube.com/watch?v=XO-7pyzmbLs" {
			t.Fatal("video URL did not use the canonical metadata endpoint", raw, endpoint)
		}
	}
	for _, raw := range []string{
		"https://www.youtube.com/@creator",
		"https://www.youtube.com/playlist?list=playlist",
		"https://www.youtube.com/watch?v=too-short",
		"https://www.youtube.com/watch?v=XO-7pyzmbL!",
		"https://www.youtube.com.evil.test/watch?v=XO-7pyzmbLs",
		"https://evil.test/youtube.com/watch?v=XO-7pyzmbLs",
		"https://youtu.be/XO-7pyzmbLs/more",
	} {
		u, _ := url.Parse(raw)
		if endpoint := youtubeEndpoint(u); endpoint != "" {
			t.Fatal("non-video or unrelated URL treated as a YouTube video", raw, endpoint)
		}
	}
}

func TestYouTubeMetadataFailuresKeepOriginalPreview(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
	}{
		{"private or removed video", "", 404},
		{"provider unavailable", "", 503},
		{"bad JSON", "<html>Consent required</html>", 200},
		{"wrong title type", `{"title":false}`, 200},
		{"no title", `{"author_name":"Author"}`, 200},
		{"invalid UTF-8", "{\"title\":\"Bad\xff\"}", 200},
		{"too large", `{"title":"Unexpected"}` + strings.Repeat(" ", 32<<10), 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			requests := 0
			client := New(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				requests++
				return &http.Response{StatusCode: tc.status, Header: http.Header{},
					Body: io.NopCloser(strings.NewReader(tc.body)), Request: r}, nil
			})})
			for range 2 {
				if card := client.Lookup(t.Context(), "https://youtu.be/XO-7pyzmbLs"); card.Title != "" || len(card.Image) != 0 {
					t.Fatal("failed metadata produced a card", card.Title)
				}
			}
			if requests != 1 {
				t.Fatal("failed video retried on every render", requests)
			}
		})
	}
}

func TestYouTubeBadThumbnailKeepsTitleAndAuthor(t *testing.T) {
	for _, thumbnail := range []string{"https://i.ytimg.com/missing.jpg", "http://127.0.0.1:8081/private"} {
		client := New(nil)
		guarded := client.http.Transport
		client.http.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
			switch r.URL.Hostname() {
			case "www.youtube.com":
				return &http.Response{StatusCode: 200, Header: http.Header{}, Request: r,
					Body: io.NopCloser(strings.NewReader(`{"title":"Video title","author_name":"Author","thumbnail_url":"` + thumbnail + `"}`))}, nil
			case "i.ytimg.com":
				return &http.Response{StatusCode: 404, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("")), Request: r}, nil
			default:
				return guarded.RoundTrip(r)
			}
		})
		if card := client.Lookup(t.Context(), "https://youtu.be/XO-7pyzmbLs"); card.Title != "Video title" || card.Description != "Author" || len(card.Image) != 0 {
			t.Fatal("unavailable or private thumbnail discarded usable video metadata", card.Title, card.Description)
		}
	}
}
