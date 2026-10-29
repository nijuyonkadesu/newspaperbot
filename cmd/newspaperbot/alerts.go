package main

import (
	"context"
	"os"
	"strconv"
	"strings"

	"github.com/go-telegram/bot"
	"newspaperbot/internal/telegram"
)

const alertPath = ".run/owner-alerts.json"

// A failed database or repository setup must not prevent contacting the owner.
func reportStartup(err error) {
	owner, parseErr := strconv.ParseInt(os.Getenv("OWNER_ID"), 10, 64)
	if parseErr != nil || owner <= 0 || os.Getenv("BOT_TOKEN") == "" {
		return
	}
	b, clientErr := bot.New(os.Getenv("BOT_TOKEN"), bot.WithSkipGetMe(),
		bot.WithServerURL(strings.TrimRight(env("BOT_API_URL", "https://api.telegram.org"), "/")))
	if clientErr != nil {
		return
	}
	app := &telegram.App{OwnerID: owner, AlertPath: alertPath}
	app.ReportError(context.Background(), b, "Startup failed", err)
}
