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
			{Command: "newpost", Description: "Create a blog post"},
			{Command: "taxonomy", Description: "Copy categories and tags from a live list"},
			{Command: "drafts", Description: "List saved drafts"},
			{Command: "resume", Description: "Resume a draft: /resume <number>"},
			{Command: "preview", Description: "Preview the active draft"},
			{Command: "publish", Description: "Save Markdown and publish to the channel"},
			{Command: "replace", Description: "Replace the post in one message"},
			{Command: "undo", Description: "Undo the last body addition"},
			{Command: "download", Description: "Download the Markdown file"},
			{Command: "cancel", Description: "Delete the active unfinished draft"},
			{Command: "delete", Description: "Delete a saved draft: /delete <number>"},
			{Command: "setchannel", Description: "Set the channel: /setchannel @name or ID"},
			{Command: "unsetchannel", Description: "Disable channel publishing for future posts"},
			{Command: "help", Description: "Show help and commands"},
			{Command: "start", Description: "Show help and refresh the command menu"},
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
