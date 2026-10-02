package telegram

import (
	"context"
	"fmt"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

// RegisterMenu exposes command suggestions and the Menu button in the owner's DM.
func (a *App) RegisterMenu(ctx context.Context, b *bot.Bot) error {
	_, err := b.SetMyCommands(ctx, &bot.SetMyCommandsParams{
		Scope: &models.BotCommandScopeChat{ChatID: a.OwnerID},
		Commands: []models.BotCommand{
			{Command: "newpost", Description: "Start a draft"},
			{Command: "taxonomy", Description: "Copy categories and grouped tags"},
			{Command: "drafts", Description: "List drafts"},
			{Command: "resume", Description: "Resume draft: /resume <number>"},
			{Command: "posts", Description: "Browse published articles"},
			{Command: "edit", Description: "Edit/resume article: /edit <number>"},
			{Command: "save", Description: "Save article edits"},
			{Command: "publish", Description: "Publish draft"},
			{Command: "replace", Description: "Replace entire post"},
			{Command: "undo", Description: "Remove last appended text; keeps the chat message"},
			{Command: "remove", Description: "Remove addition: reply or message ID"},
			{Command: "download", Description: "Download Markdown"},
			{Command: "cancel", Description: "Delete draft / discard article edits"},
			{Command: "delete", Description: "Delete draft: /delete <number>"},
			{Command: "channels", Description: "Show configured destination"},
			{Command: "setchannel", Description: "Set destination: /setchannel @name or ID"},
			{Command: "unsetchannel", Description: "Clear destination for future posts"},
			{Command: "help", Description: "Show usage"},
		},
	})
	if err != nil {
		return fmt.Errorf("register commands: %w", err)
	}
	_, err = b.SetChatMenuButton(ctx, &bot.SetChatMenuButtonParams{
		ChatID:     a.OwnerID,
		MenuButton: models.MenuButtonCommands{Type: models.MenuButtonTypeCommands},
	})
	if err != nil {
		return fmt.Errorf("set command menu button: %w", err)
	}
	return nil
}
