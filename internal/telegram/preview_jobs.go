package telegram

import (
	"context"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

type previewTarget struct {
	chat int64
	id   int
}

type previewJob struct {
	cancel context.CancelFunc
	done   chan struct{}
}

// Task registration and final edits use the existing owner-update lock.
// Cancellation itself is safe to invoke before that lock, including during upload.
func (a *App) cancelPreview(target previewTarget) {
	if job := a.previewJobs[target]; job != nil {
		job.cancel()
		delete(a.previewJobs, target)
	}
}

func (a *App) cancelOwnerPreview() {
	if job := a.ownerPreview.Load(); job != nil {
		job.cancel()
	}
}

// ClosePreviews cancels pending fetches/edits and joins them before shutdown.
func (a *App) ClosePreviews() {
	a.cancelOwnerPreview()
	a.mu.Lock()
	for target, job := range a.previewJobs {
		job.cancel()
		delete(a.previewJobs, target)
	}
	a.mu.Unlock()
	a.previewWG.Wait()
}

func (a *App) queuePreview(ctx context.Context, b *bot.Bot, target previewTarget, markup *models.InlineKeyboardMarkup, rich models.InputRichMessage, source string, footer bool) {
	if a.Previews == nil || target.id == 0 || ctx.Err() != nil {
		return
	}
	parent := a.PreviewContext
	if parent == nil {
		parent = ctx
	}
	workCtx, cancel := context.WithTimeout(parent, 15*time.Second)
	job := &previewJob{cancel: cancel, done: make(chan struct{})}
	if a.previewJobs == nil {
		a.previewJobs = map[previewTarget]*previewJob{}
	}
	if old := a.previewJobs[target]; old != nil {
		old.cancel()
	}
	a.previewJobs[target] = job
	if target.chat == a.OwnerID {
		a.ownerPreview.Store(job)
	}
	a.previewWG.Add(1)
	go func() {
		defer a.previewWG.Done()
		defer close(job.done)
		defer cancel()
		defer func() {
			a.mu.Lock()
			if a.previewJobs[target] == job {
				delete(a.previewJobs, target)
			}
			a.mu.Unlock()
			a.ownerPreview.CompareAndSwap(job, nil)
		}()
		card := a.Previews.Lookup(workCtx, source)
		if card.Title == "" || workCtx.Err() != nil {
			return
		}
		// Serialize the final edit with foreground renders. The generation check
		// also protects against transports which return after cancellation.
		a.mu.Lock()
		defer a.mu.Unlock()
		current := a.previewJobs[target] == job
		if !current || workCtx.Err() != nil {
			return
		}
		if _, err := a.writeLinkCard(workCtx, b, target, markup, &rich, card, footer); err != nil && workCtx.Err() == nil && !unchangedMessage(err) {
			a.logError(b, "URL preview edit", err)
		}
	}()
}
