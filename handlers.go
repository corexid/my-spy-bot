package main

import (
	"context"
	"fmt"
	"log"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"github.com/redis/go-redis/v9"
)

const (
	btnDemoText  = "▶️ Демонстрация работы бота"
	btnSetupText = "📌 Как подключить бота"
)

func RegisterHandlers(b *bot.Bot, cache *Cache, checker *SubscriptionChecker) {
	b.RegisterHandler(bot.HandlerTypeMessageText, "/start", bot.MatchTypeExact, func(ctx context.Context, b *bot.Bot, update *models.Update) {
		if !allowBySubscription(ctx, b, update, checker) {
			return
		}

		welcome := `Добро пожаловать!
🕵️ Этот бот создан, чтобы помогать вам в переписке.

Возможности бота:
• Моментально пришлёт уведомление, если ваш собеседник изменит или удалит сообщение 🔔
• Умеет сохранять вложения из бизнес-чатов: фото/видео/документы/голосовые ⏳

❓ Нажмите кнопку «📌 Как подключить бота», чтобы увидеть инструкцию 👇`

		_, err := b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID:      update.Message.Chat.ID,
			Text:        welcome,
			ReplyMarkup: buildMainKeyboard(),
		})
		if err != nil {
			log.Printf("failed to send /start message: %v", err)
		}
	})

	b.RegisterHandler(bot.HandlerTypeMessageText, "/ping", bot.MatchTypeExact, func(ctx context.Context, b *bot.Bot, update *models.Update) {
		if !allowBySubscription(ctx, b, update, checker) {
			return
		}
		_, _ = b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: update.Message.Chat.ID,
			Text:   "pong",
		})
	})

	b.RegisterHandler(bot.HandlerTypeMessageText, btnDemoText, bot.MatchTypeExact, func(ctx context.Context, b *bot.Bot, update *models.Update) {
		if !allowBySubscription(ctx, b, update, checker) {
			return
		}
		_, _ = b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: update.Message.Chat.ID,
			Text:   "Демо: подключите бота в Telegram Business -> Чат-боты, откройте чат клиента, отправьте сообщение и удалите его. Бот пришлёт сохранённый текст/файл.",
		})
	})

	b.RegisterHandler(bot.HandlerTypeMessageText, btnSetupText, bot.MatchTypeExact, func(ctx context.Context, b *bot.Bot, update *models.Update) {
		if !allowBySubscription(ctx, b, update, checker) {
			return
		}

		setup := `Как подключить бота в Telegram Business:
1. Откройте Telegram -> Настройки -> Telegram для бизнеса.
2. Зайдите в раздел «Чат-боты».
3. Найдите и добавьте вашего бота.
4. Дайте права на чтение/ответы и доступ к нужным чатам.
5. После подключения в чате появится статус «бот управляет этим чатом».`

		_, _ = b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: update.Message.Chat.ID,
			Text:   setup,
		})
	})
}

func DefaultHandler(ctx context.Context, b *bot.Bot, update *models.Update, cache *Cache, checker *SubscriptionChecker) {
	if update.BusinessConnection != nil {
		bc := update.BusinessConnection
		log.Printf("business connection: id=%s user_id=%d enabled=%v", bc.ID, bc.User.ID, bc.IsEnabled)
		return
	}

	if update.BusinessMessage != nil {
		m := update.BusinessMessage
		if m.BusinessConnectionID == "" {
			return
		}

		payload := extractPayload(m)
		if payload == "" {
			return
		}

		if err := cache.SaveMessage(m.BusinessConnectionID, m.ID, payload); err != nil {
			log.Printf("failed to save business message in redis: %v", err)
		}
		return
	}

	if update.EditedBusinessMessage != nil {
		m := update.EditedBusinessMessage
		if m.BusinessConnectionID == "" {
			return
		}

		newPayload := extractPayload(m)
		if newPayload == "" {
			return
		}

		oldPayload, err := cache.GetMessage(m.BusinessConnectionID, m.ID)
		if err != nil && err != redis.Nil {
			log.Printf("failed to read edited business message from redis: %v", err)
		}

		connection, err := b.GetBusinessConnection(ctx, &bot.GetBusinessConnectionParams{
			BusinessConnectionID: m.BusinessConnectionID,
		})
		if err != nil {
			log.Printf("failed to get business connection (%s): %v", m.BusinessConnectionID, err)
			return
		}

		author := formatActorFromChat(&m.Chat)
		if oldPayload != "" && oldPayload != newPayload {
			_, sendErr := b.SendMessage(ctx, &bot.SendMessageParams{
				ChatID: connection.UserChatID,
				Text: fmt.Sprintf(
					"%s изменил(а) сообщение:\n\nOld:\n\u201c%s\u201d\n\nNew:\n\u201c%s\u201d",
					author,
					oldPayload,
					newPayload,
				),
			})
			if sendErr != nil {
				log.Printf("failed to send edit notification: %v", sendErr)
			}
		}

		if err := cache.SaveMessage(m.BusinessConnectionID, m.ID, newPayload); err != nil {
			log.Printf("failed to update edited business message in redis: %v", err)
		}
		return
	}

	if update.DeletedBusinessMessages != nil {
		deleted := update.DeletedBusinessMessages
		connection, err := b.GetBusinessConnection(ctx, &bot.GetBusinessConnectionParams{
			BusinessConnectionID: deleted.BusinessConnectionID,
		})
		if err != nil {
			log.Printf("failed to get business connection (%s): %v", deleted.BusinessConnectionID, err)
			return
		}

		for _, messageID := range deleted.MessageIDs {
			cached, err := cache.GetMessage(deleted.BusinessConnectionID, messageID)
			if err != nil {
				if err == redis.Nil {
					continue
				}
				log.Printf("failed to read deleted business message from redis: %v", err)
				continue
			}

			author := formatActorFromChat(&deleted.Chat)
			_, sendErr := b.SendMessage(ctx, &bot.SendMessageParams{
				ChatID: connection.UserChatID,
				Text:   fmt.Sprintf("%s удалил(а) сообщение:\n\n%s", author, cached),
			})
			if sendErr != nil {
				log.Printf("failed to send delete notification: %v", sendErr)
			}
		}
		return
	}

	if update.Message != nil && update.Message.Text != "" {
		if !allowBySubscription(ctx, b, update, checker) {
			return
		}
		_, _ = b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: update.Message.Chat.ID,
			Text:   "Принял: " + update.Message.Text,
		})
	}
}

func allowBySubscription(ctx context.Context, b *bot.Bot, update *models.Update, checker *SubscriptionChecker) bool {
	if update == nil || update.Message == nil || update.Message.From == nil {
		return true
	}
	return checker.Ensure(ctx, b, update.Message.From.ID, update.Message.Chat.ID)
}

func buildMainKeyboard() *models.ReplyKeyboardMarkup {
	return &models.ReplyKeyboardMarkup{
		ResizeKeyboard: true,
		Keyboard: [][]models.KeyboardButton{
			{{Text: btnDemoText}},
			{{Text: btnSetupText}},
		},
	}
}

func extractPayload(m *models.Message) string {
	if m.Text != "" {
		return m.Text
	}
	if len(m.Photo) > 0 {
		return m.Photo[len(m.Photo)-1].FileID
	}
	if m.Video != nil {
		return m.Video.FileID
	}
	if m.Document != nil {
		return m.Document.FileID
	}
	if m.Audio != nil {
		return m.Audio.FileID
	}
	if m.Sticker != nil {
		return m.Sticker.FileID
	}
	if m.Voice != nil {
		return m.Voice.FileID
	}
	if m.VideoNote != nil {
		return m.VideoNote.FileID
	}
	return ""
}

func formatActorFromChat(chat *models.Chat) string {
	if chat == nil {
		return "Собеседник"
	}

	if chat.Title != "" && chat.Username != "" {
		return fmt.Sprintf("%s (@%s)", chat.Title, chat.Username)
	}
	if chat.Title != "" {
		return chat.Title
	}
	if chat.Username != "" {
		return fmt.Sprintf("@%s", chat.Username)
	}
	if chat.FirstName != "" && chat.LastName != "" {
		return chat.FirstName + " " + chat.LastName
	}
	if chat.FirstName != "" {
		return chat.FirstName
	}
	return "Собеседник"
}
