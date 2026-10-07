package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/jpeg"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/go-telegram/bot"
	"newspaperbot/internal/media"
)

func TestMediaRetrievalSupportsPublicFileURLsAndReadableLocalAPIPaths(t *testing.T) {
	var imageData bytes.Buffer
	if err := jpeg.Encode(&imageData, image.NewRGBA(image.Rect(0, 0, 2, 2)), nil); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"http", "local", "oversize", "missing", "redirect", "unreadable local"} {
		t.Run(mode, func(t *testing.T) {
			path := "photos/file.jpg"
			if mode == "local" {
				path = filepath.Join(t.TempDir(), "file.jpg")
				if err := os.WriteFile(path, imageData.Bytes(), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "unreadable local" {
				path = filepath.Join(t.TempDir(), "missing.jpg")
			}
			var fileRequests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "/getFile") {
					json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": map[string]any{"file_id": "photo", "file_unique_id": "unique", "file_path": path}})
					return
				}
				fileRequests.Add(1)
				switch mode {
				case "missing":
					http.Error(w, "gone", 404)
				case "oversize":
					w.Write(bytes.Repeat([]byte{'x'}, media.MaxImageBytes+1))
				case "redirect":
					http.Redirect(w, r, "http://unreachable.test/token", 302)
				default:
					w.Write(imageData.Bytes())
				}
			}))
			defer server.Close()
			b, err := bot.New("123:test", bot.WithSkipGetMe(), bot.WithServerURL(server.URL))
			if err != nil {
				t.Fatal(err)
			}
			data, err := DownloadMedia(context.Background(), b, "photo")
			if mode == "http" || mode == "local" {
				if err != nil || !bytes.Equal(data, imageData.Bytes()) {
					t.Fatal("media bytes changed or retrieval failed", err)
				}
				if mode == "local" && fileRequests.Load() != 0 {
					t.Fatal("local file was unnecessarily downloaded via HTTP")
				}
			} else if err == nil || strings.Contains(err.Error(), b.Token()) || strings.Contains(err.Error(), path) {
				t.Fatal("failed download succeeded or leaked the API path/token", err)
			}
		})
	}
}
