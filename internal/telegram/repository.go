package telegram

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/go-telegram/bot"
	"newspaperbot/internal/media"
	"newspaperbot/internal/post"
	"newspaperbot/internal/store"
)

func (a *App) queuePublication(ctx context.Context, b *bot.Bot, d *post.Draft) error {
	if err := d.ValidatePortfolio(); err != nil {
		return a.notice(ctx, b, d, err.Error())
	}
	if d.GitOperation == "" && time.Since(d.AlbumUpdatedAt) < 750*time.Millisecond {
		if d.PublishRequestedAt.IsZero() {
			d.PublishRequestedAt = time.Now().UTC()
		}
		return a.notice(ctx, b, d, "Publishing after album arrives…")
	}
	channel, err := a.Store.Setting(ctx, "channel")
	if err != nil {
		return err
	}
	if d.GitOperation != "" {
		channel = d.ChannelID
	}
	if channel != 0 {
		if err := a.checkDestination(ctx, b, channel); err != nil {
			return a.notice(ctx, b, d, err.Error())
		}
	}
	*d, err = a.Store.Queue(ctx, d.ID, channel)
	if err != nil {
		return err
	}
	return a.render(ctx, b, d)
}

// Git/npm work runs outside the owner-update lock. Only the final card merge and
// channel delivery are serialized with owner actions. Pending jobs survive exit.
func (a *App) RunPublisher(ctx context.Context, b *bot.Bot) {
	if a.Repository == nil {
		return
	}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		a.flushMediaRequests(ctx, b)
		jobs, err := a.Store.PendingPublications(ctx)
		if err != nil && ctx.Err() == nil {
			a.logError(b, "publication queue", err)
		}
		for i := range jobs {
			if ctx.Err() != nil {
				return
			}
			a.runPublication(ctx, b, &jobs[i])
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (a *App) runPublication(ctx context.Context, b *bot.Bot, job *store.Publication) {
	workCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	err := a.Repository.Publish(workCtx, job, func() error { return a.Store.SavePublication(workCtx, job) })
	if ctx.Err() != nil {
		return
	} // Leave it resumable; never turn shutdown into a failed job.
	a.mu.Lock()
	defer a.mu.Unlock()
	uiCtx, cancelUI := context.WithTimeout(ctx, 30*time.Second)
	defer cancelUI()
	if err != nil {
		a.logError(b, "repository publication", err)
		job.Error = err.Error()
		job.Conflict = errors.Is(err, post.ErrArticleChanged)
		var imageErr *media.ImageError
		var d post.Draft
		var saveErr error
		if errors.As(err, &imageErr) && job.CommitSHA == "" {
			d, saveErr = a.Store.ReleasePublication(uiCtx, job, imageErr.FileID, imageErr.Reason)
		} else {
			d, saveErr = a.Store.FailPublication(uiCtx, job)
		}
		if saveErr != nil {
			a.logError(b, "publication checkpoint", saveErr)
			return
		}
		if renderErr := a.render(uiCtx, b, &d); renderErr != nil {
			a.logError(b, "publication card", renderErr)
		}
		return
	}
	d, err := a.Store.FinishPublication(uiCtx, job)
	if err != nil {
		a.logError(b, "publication completion", err)
		return
	}
	if err := a.publish(uiCtx, b, &d, false); err != nil {
		a.logError(b, "publication destination", err)
		status := "Committed to main. Continue publishing to finish destination delivery."
		if d.Revision != nil {
			status = "Repository updated · channel update pending"
		}
		_ = a.notice(uiCtx, b, &d, status)
	} else if err := a.applyLateMedia(uiCtx, b, &d); err != nil {
		a.logError(b, "late album", err)
	}
}

func (a *App) flushMediaRequests(ctx context.Context, b *bot.Bot) {
	drafts, err := a.Store.MediaPending(ctx)
	if err != nil {
		if ctx.Err() == nil {
			a.logError(b, "media pending", err)
		}
		return
	}
	for _, saved := range drafts {
		a.mu.Lock()
		d, err := a.Store.Get(ctx, saved.ID)
		if err == nil {
			if len(d.LateMedia) != 0 && d.Exported && d.Revision == nil && (d.ChannelID == 0 || d.Delivery == "sent") {
				err = a.applyLateMedia(ctx, b, &d)
			}
			if err == nil && !d.PublishRequestedAt.IsZero() && time.Since(d.AlbumUpdatedAt) >= 750*time.Millisecond {
				if validation := d.ValidatePortfolio(); validation != nil {
					d.PublishRequestedAt = time.Time{}
					err = a.notice(ctx, b, &d, validation.Error())
				} else if d.Revision != nil {
					err = a.saveChanges(ctx, b, &d)
				} else {
					err = a.queuePublication(ctx, b, &d)
				}
			}
		}
		a.mu.Unlock()
		if err != nil && !errors.Is(err, sql.ErrNoRows) && ctx.Err() == nil {
			a.logError(b, "media pending", err)
		}
	}
}
