package telegram

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-telegram/bot"
	"newspaperbot/internal/post"
	"newspaperbot/internal/store"
)

func TestActionFailureUpdatesCardWithoutNewMessages(t *testing.T) {
	h := newHarness(t)
	d := h.ready("Body")
	before := h.api.count("sendMessage", false)
	h.app.WriteFile = func(string, []byte) error { return errors.New("disk full") }
	h.send("/publish")
	if h.active().CardID != d.CardID || h.api.count("sendMessage", false) != before {
		t.Fatal("an error replaced the card or sent an unnecessary DM")
	}
	h.api.mu.Lock()
	text := h.api.messages[d.CardID].Text
	h.api.mu.Unlock()
	if !strings.Contains(text, "Action failed · disk full") {
		t.Fatal("the card hid the actual failure", text)
	}
}

func TestDatabaseFailureReachesOwnerWithoutDatabase(t *testing.T) {
	h := newHarness(t)
	h.ready("Body")
	before := h.api.count("sendMessage", false)
	if err := h.app.Store.Close(); err != nil {
		t.Fatal(err)
	}
	h.send("/publish")
	h.send("/publish")
	calls := h.api.snapshot()
	last := calls[len(calls)-1]
	if h.api.count("sendMessage", false) != before+1 || last.ChatID != h.app.OwnerID || !strings.Contains(last.Text, "database is closed") {
		t.Fatal("database failure was silent or repeated alerts spammed the owner", last)
	}
}

func TestCardFailureFallsBackToOwnerDM(t *testing.T) {
	h := newHarness(t)
	h.ready("Body")
	h.app.WriteFile = func(string, []byte) error { return errors.New("disk full") }
	b, err := bot.New("123:test", bot.WithSkipGetMe(), bot.WithServerURL("https://telegram.test"),
		bot.WithHTTPClient(time.Second, &http.Client{Transport: previewTransport(func(r *http.Request) (*http.Response, error) {
			if filepath.Base(r.URL.Path) == "editMessageText" {
				h.api.mu.Lock()
				h.api.editError = "edit unavailable"
				h.api.mu.Unlock()
			}
			return h.api.RoundTrip(r)
		})}))
	if err != nil {
		t.Fatal(err)
	}
	h.bot = b
	h.send("/publish")
	calls := h.api.snapshot()
	last := calls[len(calls)-1]
	if last.Method != "sendMessage" || last.ChatID != h.app.OwnerID || !strings.Contains(last.Text, "disk full") || !strings.Contains(last.Text, "Title") {
		t.Fatal("failed card edit swallowed the actionable owner alert", last)
	}
}

func TestAlertsReuseMessagesAcrossConcurrencyRestartAndRecovery(t *testing.T) {
	h := newHarness(t)
	h.app.AlertPath = filepath.Join(t.TempDir(), "state", "alerts.json")
	err := errors.New("git fetch failed")
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Go(func() { h.app.ReportError(context.Background(), h.bot, "Repository refresh", err) })
	}
	wg.Wait()
	if h.api.count("sendMessage", false) != 1 {
		t.Fatal("concurrent errors created duplicate messages")
	}
	restarted := &App{OwnerID: h.app.OwnerID, AlertPath: h.app.AlertPath}
	restarted.ReportError(context.Background(), h.bot, "Repository refresh", err)
	restarted.ReportError(context.Background(), h.bot, "Repository refresh", nil)
	restarted.ReportError(context.Background(), h.bot, "Repository refresh", nil)
	restarted.ReportError(context.Background(), h.bot, "Repository refresh", err)
	if h.api.count("sendMessage", false) != 1 || h.api.count("editMessageText", false) != 2 {
		t.Fatal("restart or recovery duplicated the alert instead of editing it")
	}
	h.api.mu.Lock()
	delete(h.api.messages, h.api.calls[0].ResultID)
	h.api.mu.Unlock()
	restarted.ReportError(context.Background(), h.bot, "Repository refresh", errors.New("git fetch timed out"))
	if h.api.count("sendMessage", false) != 2 {
		t.Fatal("a deleted alert prevented delivering the next failure")
	}
}

func TestAlertsRedactSecretsAndHandleBadReceiptsAndTimeouts(t *testing.T) {
	h := newHarness(t)
	h.app.AlertPath = filepath.Join(t.TempDir(), "alerts.json")
	if err := os.WriteFile(h.app.AlertPath, []byte("null"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PORTFOLIO_GIT_TOKEN", "private-git-secret")
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	h.app.ReportError(ctx, h.bot, "<Failure>", fmt.Errorf("credentials %s private-git-secret <unsafe>", h.bot.Token()))
	data, err := os.ReadFile(h.app.AlertPath)
	if err != nil {
		t.Fatal(err)
	}
	calls := h.api.snapshot()
	text := calls[len(calls)-1].Text
	for _, secret := range []string{h.bot.Token(), "private-git-secret"} {
		if strings.Contains(text+string(data), secret) {
			t.Fatal("an alert or its receipt exposed a credential")
		}
	}
	if !strings.Contains(text, "&lt;Failure&gt;") || !strings.Contains(text, "&lt;unsafe&gt;") {
		t.Fatal("error text was interpreted as HTML", text)
	}
	shutdown, stop := context.WithCancel(context.Background())
	stop()
	before := len(calls)
	h.app.ReportError(shutdown, h.bot, "Shutdown", context.Canceled)
	if len(h.api.snapshot()) != before {
		t.Fatal("normal shutdown produced an error alert")
	}
}

func TestFailedAlertSendCanBeRetried(t *testing.T) {
	h := newHarness(t)
	failed := false
	b, err := bot.New("123:test", bot.WithSkipGetMe(), bot.WithServerURL("https://telegram.test"),
		bot.WithHTTPClient(time.Second, &http.Client{Transport: previewTransport(func(r *http.Request) (*http.Response, error) {
			if !failed {
				failed = true
				return nil, errors.New("temporary network failure")
			}
			return h.api.RoundTrip(r)
		})}))
	if err != nil {
		t.Fatal(err)
	}
	failure := errors.New("database unavailable")
	h.app.ReportError(context.Background(), b, "Publication queue", failure)
	h.app.ReportError(context.Background(), b, "Publication queue", failure)
	if h.api.count("sendMessage", false) != 1 {
		t.Fatal("a failed send was incorrectly recorded as a delivered alert")
	}
}

type failedCheckpointRepository struct {
	*fakeRepository
	db *store.Store
}

func (r failedCheckpointRepository) Publish(ctx context.Context, job *store.Publication, checkpoint func() error) error {
	if err := r.fakeRepository.Publish(ctx, job, checkpoint); err != nil {
		return err
	}
	return r.db.Close()
}

func TestPostPushCheckpointFailureNotifiesOwner(t *testing.T) {
	h := newHarness(t)
	h.app.Repository = failedCheckpointRepository{fakeRepository: &fakeRepository{}, db: h.app.Store}
	h.send("/setchannel @first")
	h.ready("Body")
	h.send("/publish")
	jobs, err := h.app.Store.PendingPublications(context.Background())
	if err != nil || len(jobs) != 1 {
		t.Fatal("missing publish job", err)
	}
	h.app.runPublication(context.Background(), h.bot, &jobs[0])
	calls := h.api.snapshot()
	last := calls[len(calls)-1]
	if last.ChatID != h.app.OwnerID || !strings.Contains(last.Text, "Publication checkpoint failed") || !strings.Contains(last.Text, "database is closed") {
		t.Fatal("a successful push followed by a failed SQLite checkpoint stayed silent", last)
	}
	if h.api.count("sendRichMessage", true) != 0 {
		t.Fatal("channel delivery started before the Git checkpoint was saved")
	}
}

type unavailableAlbumRepository struct{ *fakeRepository }

func (unavailableAlbumRepository) Articles(context.Context) ([]post.Article, error) {
	return nil, errors.New("article unavailable")
}

func TestBackgroundAlbumFailureSerializesCardWithOwnerEdits(t *testing.T) {
	h := newHarness(t)
	h.app.Repository = unavailableAlbumRepository{&fakeRepository{}}
	h.ready("Body")
	h.send("/publish")
	h.finishGit()
	d := h.active()
	d.LateMedia = []post.Source{{MessageID: 99, Media: &post.Media{Kind: "photo", FileID: "late-photo"}}}
	if err := h.app.Store.Save(context.Background(), &d); err != nil {
		t.Fatal(err)
	}
	checked := false
	b, err := bot.New("123:test", bot.WithSkipGetMe(), bot.WithServerURL("https://telegram.test"),
		bot.WithHTTPClient(time.Second, &http.Client{Transport: previewTransport(func(r *http.Request) (*http.Response, error) {
			if filepath.Base(r.URL.Path) == "editMessageText" && strings.Contains(r.FormValue("text"), "Album update failed") {
				checked = true
				if h.app.mu.TryLock() {
					h.app.mu.Unlock()
					t.Error("background error card can race with owner edits and preview jobs")
				}
			}
			return h.api.RoundTrip(r)
		})}))
	if err != nil {
		t.Fatal(err)
	}
	h.app.flushMediaRequests(context.Background(), b)
	if !checked {
		t.Fatal("background album failure was not shown on its card")
	}
}
