package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestStartupDatabaseFailureAlertsWithoutDatabaseAndDeduplicatesRestarts(t *testing.T) {
	t.Chdir(t.TempDir())
	var methods, messages []string
	var mu sync.Mutex
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Error(err)
		}
		if r.MultipartForm != nil {
			defer r.MultipartForm.RemoveAll()
		}
		methods = append(methods, filepath.Base(r.URL.Path))
		messages = append(messages, r.FormValue("text"))
		if r.FormValue("chat_id") != "42" {
			t.Error("startup error was not addressed to the owner")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":100,"chat":{"id":42,"type":"private"}}}`))
	}))
	defer server.Close()
	t.Setenv("BOT_TOKEN", "123:private")
	t.Setenv("OWNER_ID", "42")
	t.Setenv("BOT_API_URL", server.URL)
	t.Setenv("DB_PATH", t.TempDir()) // A directory cannot be opened as SQLite.
	err := run()
	if err == nil {
		t.Fatal("startup accepted an unusable database")
	}
	reportStartup(err)
	reportStartup(err)
	mu.Lock()
	defer mu.Unlock()
	if len(methods) != 1 || methods[0] != "sendMessage" || !strings.Contains(messages[0], "open database") {
		t.Fatal("startup failure was hidden, duplicated, or required a working database", methods, messages)
	}
	if _, err := os.Stat(alertPath); err != nil {
		t.Fatal("startup notification did not save its delivery receipt", err)
	}
}
