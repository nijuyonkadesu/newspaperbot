package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"tgblogbot/internal/metadata"
	"tgblogbot/internal/post"
	"tgblogbot/internal/store"
	"tgblogbot/internal/telegram"
)

func main() {
	if err := run(); err != nil {
		message := err.Error()
		if token := os.Getenv("BOT_TOKEN"); token != "" {
			message = strings.ReplaceAll(message, token, "[redacted]")
		}
		log.Print(message)
		os.Exit(1)
	}
}

func run() error {
	token := os.Getenv("BOT_TOKEN")
	if token == "" {
		return errors.New("BOT_TOKEN is required")
	}
	owner, err := strconv.ParseInt(os.Getenv("OWNER_ID"), 10, 64)
	if err != nil || owner <= 0 {
		return errors.New("OWNER_ID must be your positive numeric Telegram user ID")
	}
	dir, err := os.Getwd()
	if err != nil {
		return err
	}
	db, err := store.Open(env("DB_PATH", "blogbot.db"))
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer db.Close()
	app := &telegram.App{OwnerID: owner, Store: db, OutputDir: dir, WriteFile: post.WriteFile, Metadata: metadata.Loader{
		NumberSource:     env("BLOG_NUMBER_SOURCE", "testdata/blog-number.json"),
		CategoriesSource: env("CATEGORIES_SOURCE", "testdata/categories.json"),
		TagsSource:       env("TAGS_SOURCE", "testdata/tags.json"),
	}}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	b, err := bot.New(token, bot.WithDefaultHandler(app.Handle), bot.WithNotAsyncHandlers(), bot.WithWorkers(1),
		bot.WithAllowedUpdates(bot.AllowedUpdates{models.AllowedUpdateMessage, models.AllowedUpdateEditedMessage, models.AllowedUpdateCallbackQuery}),
		bot.WithErrorsHandler(func(err error) { log.Print(strings.ReplaceAll(err.Error(), token, "[redacted]")) }))
	if err != nil {
		return err
	}
	menuCtx, cancelMenu := context.WithTimeout(ctx, 15*time.Second)
	if err := app.RegisterMenu(menuCtx, b); err != nil {
		// The owner may not have opened the DM yet. /start retries registration.
		log.Printf("command menu: %s", strings.ReplaceAll(err.Error(), token, "[redacted]"))
	}
	cancelMenu()
	cardCtx, cancelCard := context.WithTimeout(ctx, 15*time.Second)
	if err := app.RestoreCard(cardCtx, b); err != nil {
		log.Printf("restore draft card: %s", strings.ReplaceAll(err.Error(), token, "[redacted]"))
	}
	cancelCard()
	log.Print("blog bot started; authoring restricted to the owner's private chat")
	syncDone := make(chan struct{})
	go func() { defer close(syncDone); app.RunTaxonomySync(ctx, b) }()
	b.Start(ctx)
	stop()
	<-syncDone
	return nil
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
