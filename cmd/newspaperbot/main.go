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
	"newspaperbot/internal/linkpreview"
	"newspaperbot/internal/metadata"
	"newspaperbot/internal/post"
	"newspaperbot/internal/repository"
	"newspaperbot/internal/store"
	"newspaperbot/internal/telegram"
)

func main() {
	if err := run(); err != nil {
		message := err.Error()
		for _, key := range []string{"BOT_TOKEN", "PORTFOLIO_GIT_TOKEN"} {
			if token := os.Getenv(key); token != "" {
				message = strings.ReplaceAll(message, token, "[redacted]")
			}
		}
		log.Print(message)
		reportStartup(err)
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
	hashtags, configured := os.LookupEnv("PUBLISH_HASHTAGS")
	if !configured {
		hashtags = "#useful"
	}
	app := &telegram.App{OwnerID: owner, Store: db, OutputDir: dir, WriteFile: post.WriteFile, Previews: linkpreview.New(nil), Metadata: metadata.Loader{
		NumberSource:     env("BLOG_NUMBER_SOURCE", "testdata/blog-number.json"),
		CategoriesSource: env("CATEGORIES_SOURCE", "testdata/categories.json"),
		TagsSource:       env("TAGS_SOURCE", "testdata/tags.json"),
	}}
	app.PublishHashtags = hashtags
	app.AlertPath = alertPath
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	app.PreviewContext = ctx
	defer app.ClosePreviews()
	var b *bot.Bot
	b, err = bot.New(token, bot.WithServerURL(strings.TrimRight(env("BOT_API_URL", "https://api.telegram.org"), "/")),
		bot.WithDefaultHandler(app.Handle), bot.WithNotAsyncHandlers(), bot.WithWorkers(1),
		bot.WithAllowedUpdates(bot.AllowedUpdates{models.AllowedUpdateMessage, models.AllowedUpdateEditedMessage, models.AllowedUpdateCallbackQuery}),
		bot.WithErrorsHandler(func(err error) {
			if b != nil && strings.HasPrefix(err.Error(), "error get updates") {
				app.ReportError(ctx, b, "Telegram updates", err)
			} else {
				log.Print(strings.ReplaceAll(err.Error(), token, "[redacted]"))
			}
		}))
	if err != nil {
		return err
	}
	if os.Getenv("PORTFOLIO_GIT_TOKEN") != "" || os.Getenv("PORTFOLIO_REPO_URL") != "" {
		setupCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
		repo, err := repository.Open(setupCtx, repository.Config{
			URL:      env("PORTFOLIO_REPO_URL", "https://github.com/nijuyonkadesu/portfolio.git"),
			CacheDir: env("PORTFOLIO_CACHE_DIR", ".run/portfolio"), Token: os.Getenv("PORTFOLIO_GIT_TOKEN"),
			DownloadMedia: func(ctx context.Context, id string) ([]byte, error) { return telegram.DownloadMedia(ctx, b, id) },
		})
		cancel()
		if err != nil {
			return fmt.Errorf("portfolio setup: %w", err)
		}
		defer repo.Close()
		app.Repository = repo
		catalog, err := repo.Catalog(ctx)
		if err != nil {
			return err
		}
		log.Printf("portfolio connected: %d categories, %d tags, next note %d", len(catalog.Categories), len(catalog.Tags), catalog.LastNumber+1)
	}
	menuCtx, cancelMenu := context.WithTimeout(ctx, 15*time.Second)
	// The owner may not have opened the DM yet. /start retries registration.
	app.ReportError(ctx, b, "Command menu", app.RegisterMenu(menuCtx, b))
	cancelMenu()
	cardCtx, cancelCard := context.WithTimeout(ctx, 15*time.Second)
	app.ReportError(ctx, b, "Restore draft cards", app.RestoreCard(cardCtx, b))
	cancelCard()
	onlineCtx, cancelOnline := context.WithTimeout(ctx, 15*time.Second)
	_, err = b.SendMessage(onlineCtx, &bot.SendMessageParams{ChatID: owner, Text: "I'm online."})
	cancelOnline()
	if err != nil {
		return fmt.Errorf("send startup notification: %w", err)
	}
	app.ReportError(ctx, b, "Startup failed", nil)
	log.Print("blog bot started; authoring restricted to the owner's private chat")
	syncDone := make(chan struct{})
	go func() { defer close(syncDone); app.RunTaxonomySync(ctx, b) }()
	publishDone := make(chan struct{})
	go func() { defer close(publishDone); app.RunPublisher(ctx, b) }()
	b.Start(ctx)
	stop()
	<-syncDone
	<-publishDone
	return nil
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
