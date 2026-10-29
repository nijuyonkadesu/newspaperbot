package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"newspaperbot/internal/post"
)

type errorAlert struct {
	ID     int
	Text   string
	Failed bool
}

func errorMessage(b *bot.Bot, err error) string {
	text := err.Error()
	for _, secret := range []string{b.Token(), os.Getenv("PORTFOLIO_GIT_TOKEN")} {
		if secret != "" {
			text = strings.ReplaceAll(text, secret, "[redacted]")
		}
	}
	return clip(text, 500)
}

// ReportError works without SQLite. Each component reuses one owner-only alert;
// nil marks a reported failure resolved. Cancellation during shutdown is quiet.
func (a *App) ReportError(ctx context.Context, b *bot.Bot, label string, err error) {
	ctx = a.errorContext(ctx)
	if ctx.Err() != nil {
		return
	}
	if err != nil {
		a.logError(b, label, err)
	}
	a.alertMu.Lock()
	defer a.alertMu.Unlock()
	if a.alerts == nil {
		a.alerts = map[string]errorAlert{}
		if data, readErr := os.ReadFile(a.AlertPath); readErr == nil {
			var saved map[string]errorAlert
			if json.Unmarshal(data, &saved) == nil && saved != nil {
				a.alerts = saved
			}
		}
	}
	previous := a.alerts[label]
	if err == nil && !previous.Failed {
		return
	}
	text := "<b>" + html.EscapeString(label) + "</b> · restored"
	if err != nil {
		text = "<b>" + html.EscapeString(label) + "</b>\n" + html.EscapeString(errorMessage(b, err))
	}
	if text == previous.Text {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	var notifyErr error
	if previous.ID != 0 {
		_, notifyErr = b.EditMessageText(ctx, &bot.EditMessageTextParams{ChatID: a.OwnerID, MessageID: previous.ID, Text: text, ParseMode: models.ParseModeHTML})
	}
	if previous.ID == 0 || notifyErr != nil && !unchangedMessage(notifyErr) {
		message, sendErr := b.SendMessage(ctx, &bot.SendMessageParams{ChatID: a.OwnerID, Text: text, ParseMode: models.ParseModeHTML})
		if sendErr != nil {
			a.logError(b, "owner alert", sendErr)
			return
		}
		previous.ID = message.ID
	}
	a.alerts[label] = errorAlert{ID: previous.ID, Text: text, Failed: err != nil}
	if a.AlertPath != "" {
		// This small delivery receipt is independent of the database it reports on.
		data, _ := json.Marshal(a.alerts)
		if saveErr := a.saveAlerts(data); saveErr != nil {
			a.logError(b, "alert receipt", saveErr)
		}
	}
}

// An operation timeout must not also cancel its error notification.
func (a *App) errorContext(ctx context.Context) context.Context {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		if a.PreviewContext != nil {
			return a.PreviewContext
		}
		return context.WithoutCancel(ctx)
	}
	return ctx
}

func (a *App) saveAlerts(data []byte) error {
	if err := os.MkdirAll(filepath.Dir(a.AlertPath), 0700); err != nil {
		return err
	}
	if err := os.WriteFile(a.AlertPath+".tmp", data, 0600); err != nil {
		return err
	}
	return os.Rename(a.AlertPath+".tmp", a.AlertPath)
}

func (a *App) reportDraftError(ctx context.Context, b *bot.Bot, d *post.Draft, label string, err error) {
	ctx = a.errorContext(ctx)
	if ctx.Err() != nil {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	a.logError(b, label, err)
	// Reload current presentation; background jobs contain a frozen snapshot.
	if current, loadErr := a.Store.Get(ctx, d.ID); loadErr == nil {
		*d = current
		if notifyErr := a.notice(ctx, b, d, label+" · "+errorMessage(b, err)); notifyErr == nil {
			return
		} else {
			a.logError(b, "error card", notifyErr)
		}
	}
	a.ReportError(ctx, b, label, fmt.Errorf("%s: %w", d.Title, err))
}
