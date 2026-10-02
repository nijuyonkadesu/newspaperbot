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
			{Command: "posts", Description: "Browse live articles"},
			{Command: "edit", Description: "Edit a live article: /edit <number>"},
			{Command: "save", Description: "Save changes to the live article"},
			{Command: "preview", Description: "Preview the active draft"},
			{Command: "publish", Description: "Save Markdown and publish"},
			{Command: "replace", Description: "Replace the post in one message"},
			{Command: "undo", Description: "Undo the last body addition"},
			{Command: "remove", Description: "Remove a source: reply or message ID"},
			{Command: "download", Description: "Download the Markdown file"},
			{Command: "cancel", Description: "Cancel a draft or discard article changes"},
			{Command: "delete", Description: "Delete a saved draft: /delete <number>"},
			{Command: "channels", Description: "Show the publishing destination"},
			{Command: "setchannel", Description: "Set destination: /setchannel @name or ID"},
			{Command: "unsetchannel", Description: "Clear the publishing destination"},
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
