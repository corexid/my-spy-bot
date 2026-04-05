package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"github.com/redis/go-redis/v9"
)

const (
	btnDemoText  = "▶️ Демонстрация работы бота"
	btnSetupText = "📌 Как подключить бота"
	cbCheckSub   = "check_sub"
)

var botStartedAt time.Time

type messageSnapshot struct {
	Type    string `json:"type"`
	Text    string `json:"text,omitempty"`
	FileID  string `json:"file_id,omitempty"`
	Caption string `json:"caption,omitempty"`
}

func RegisterHandlers(b *bot.Bot, cache *Cache, checker *SubscriptionChecker) {
	b.RegisterHandler(bot.HandlerTypeMessageText, "/start", bot.MatchTypeExact, func(ctx context.Context, b *bot.Bot, update *models.Update) {
		if !markUpdateOnce(cache, update) {
			return
		}

		welcome := `Добро пожаловать!
🕵️ Этот бот помогает в переписке:
• уведомляет, если собеседник изменил или удалил сообщение;
• сохраняет медиа из бизнес-чатов (фото, видео, голосовые, кружочки и другое).

Нажмите «📌 Как подключить бота», чтобы увидеть инструкцию.`

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
		if !markUpdateOnce(cache, update) {
			return
		}
		if !allowBySubscription(ctx, b, update, checker) {
			return
		}
		_, _ = b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: update.Message.Chat.ID,
			Text:   "pong",
		})
	})

	b.RegisterHandler(bot.HandlerTypeMessageText, "/status", bot.MatchTypeExact, func(ctx context.Context, b *bot.Bot, update *models.Update) {
		if !markUpdateOnce(cache, update) {
			return
		}
		if !allowBySubscription(ctx, b, update, checker) {
			return
		}

		redisStatus := "ok"
		if err := cache.Ping(); err != nil {
			redisStatus = "error: " + err.Error()
		}

		channelConfigured := "нет"
		if checker.chatRef() != nil {
			channelConfigured = "да"
		}

		uptime := time.Since(botStartedAt).Round(time.Second)
		statusText := fmt.Sprintf("Статус бота:\n• Uptime: %s\n• Redis: %s\n• Проверка подписки: %s", uptime, redisStatus, channelConfigured)
		_, _ = b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: update.Message.Chat.ID,
			Text:   statusText,
		})
	})

	b.RegisterHandler(bot.HandlerTypeMessageText, btnDemoText, bot.MatchTypeExact, func(ctx context.Context, b *bot.Bot, update *models.Update) {
		if !markUpdateOnce(cache, update) {
			return
		}
		if !allowBySubscription(ctx, b, update, checker) {
			return
		}
		_, _ = b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: update.Message.Chat.ID,
			Text:   "Демо: подключите бота в Telegram Business -> Чат-боты, отправьте сообщение в бизнес-чате и попробуйте изменить/удалить его.",
		})
	})

	b.RegisterHandler(bot.HandlerTypeMessageText, btnSetupText, bot.MatchTypeExact, func(ctx context.Context, b *bot.Bot, update *models.Update) {
		if !markUpdateOnce(cache, update) {
			return
		}
		_, _ = b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: update.Message.Chat.ID,
			Text:   "Чтобы получить инструкцию, подпишитесь на канал и нажмите «Проверить подписку».",
			ReplyMarkup: checker.subscribeMarkup(),
		})
	})

	b.RegisterHandler(bot.HandlerTypeCallbackQueryData, cbCheckSub, bot.MatchTypeExact, func(ctx context.Context, b *bot.Bot, update *models.Update) {
		if !markUpdateOnce(cache, update) {
			return
		}
		if update == nil || update.CallbackQuery == nil {
			return
		}

		if update.CallbackQuery.Message.Message == nil {
			return
		}

		chatID := update.CallbackQuery.Message.Message.Chat.ID
		userID := update.CallbackQuery.From.ID
		if !checker.Ensure(ctx, b, userID, chatID) {
			_, _ = b.AnswerCallbackQuery(ctx, &bot.AnswerCallbackQueryParams{
				CallbackQueryID: update.CallbackQuery.ID,
				Text:            "Подписка не подтверждена",
				ShowAlert:       false,
			})
			return
		}

		setup := `Как подключить бота в Telegram Business:
1. Откройте Telegram -> Настройки -> Telegram для бизнеса.
2. Зайдите в раздел «Чат-боты».
3. Добавьте этого бота.
4. Дайте права на чтение/ответы и доступ к нужным чатам.
5. В диалоге должен появиться статус «бот управляет этим чатом».`

		_, _ = b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: chatID,
			Text:   setup,
		})
		_, _ = b.AnswerCallbackQuery(ctx, &bot.AnswerCallbackQueryParams{
			CallbackQueryID: update.CallbackQuery.ID,
			Text:            "Подписка подтверждена",
			ShowAlert:       false,
		})
	})
}

func DefaultHandler(ctx context.Context, b *bot.Bot, update *models.Update, cache *Cache, checker *SubscriptionChecker) {
	if update == nil {
		return
	}

	// Deduplicate repeated deliveries by update ID.
	ok, err := cache.MarkUpdateProcessed(update.ID)
	if err != nil {
		log.Printf("failed to mark update processed: %v", err)
	} else if !ok {
		return
	}

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

		snapshot := makeSnapshot(m)
		if snapshot == nil {
			return
		}

		raw, err := json.Marshal(snapshot)
		if err != nil {
			log.Printf("failed to marshal message snapshot: %v", err)
			return
		}

		if err := cache.SaveMessage(m.BusinessConnectionID, m.ID, string(raw)); err != nil {
			log.Printf("failed to save business message in redis: %v", err)
		}
		return
	}

	if update.EditedBusinessMessage != nil {
		m := update.EditedBusinessMessage
		if m.BusinessConnectionID == "" {
			return
		}

		newSnapshot := makeSnapshot(m)
		if newSnapshot == nil {
			return
		}

		oldSnapshot, _ := getSnapshot(cache, m.BusinessConnectionID, m.ID)
		connection, err := b.GetBusinessConnection(ctx, &bot.GetBusinessConnectionParams{
			BusinessConnectionID: m.BusinessConnectionID,
		})
		if err != nil {
			log.Printf("failed to get business connection (%s): %v", m.BusinessConnectionID, err)
			return
		}

		if !checker.Ensure(ctx, b, connection.User.ID, connection.UserChatID) {
			return
		}

		author := formatActorFromChat(&m.Chat)
		if oldSnapshot != nil && oldSnapshot.Text != "" && newSnapshot.Text != "" && oldSnapshot.Text != newSnapshot.Text {
			notifyOnce(cache, "edit", m.BusinessConnectionID, m.ID, func() {
				_, sendErr := b.SendMessage(ctx, &bot.SendMessageParams{
					ChatID: connection.UserChatID,
					Text: fmt.Sprintf(
						"%s изменил(а) сообщение:\n\nOld:\n«%s»\n\nNew:\n«%s»",
						author,
						oldSnapshot.Text,
						newSnapshot.Text,
					),
				})
				if sendErr != nil {
					log.Printf("failed to send edit notification: %v", sendErr)
				}
			})
		}

		raw, err := json.Marshal(newSnapshot)
		if err != nil {
			log.Printf("failed to marshal edited snapshot: %v", err)
			return
		}
		if err := cache.SaveMessage(m.BusinessConnectionID, m.ID, string(raw)); err != nil {
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

		if !checker.Ensure(ctx, b, connection.User.ID, connection.UserChatID) {
			return
		}

		for _, messageID := range deleted.MessageIDs {
			snapshot, err := getSnapshot(cache, deleted.BusinessConnectionID, messageID)
			if err != nil {
				if err != redis.Nil {
					log.Printf("failed to read deleted business message from redis: %v", err)
				}
				continue
			}

			author := formatActorFromChat(&deleted.Chat)
			notifyOnce(cache, "delete", deleted.BusinessConnectionID, messageID, func() {
				if sendErr := sendDeletedSnapshot(ctx, b, connection.UserChatID, author, snapshot); sendErr != nil {
					log.Printf("failed to send delete notification: %v", sendErr)
				}
			})
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

func makeSnapshot(m *models.Message) *messageSnapshot {
	if m.Text != "" {
		return &messageSnapshot{Type: "text", Text: m.Text}
	}
	if len(m.Photo) > 0 {
		return &messageSnapshot{Type: "photo", FileID: m.Photo[len(m.Photo)-1].FileID, Caption: m.Caption}
	}
	if m.Video != nil {
		return &messageSnapshot{Type: "video", FileID: m.Video.FileID, Caption: m.Caption}
	}
	if m.Document != nil {
		return &messageSnapshot{Type: "document", FileID: m.Document.FileID, Caption: m.Caption}
	}
	if m.Audio != nil {
		return &messageSnapshot{Type: "audio", FileID: m.Audio.FileID, Caption: m.Caption}
	}
	if m.Sticker != nil {
		return &messageSnapshot{Type: "sticker", FileID: m.Sticker.FileID}
	}
	if m.Voice != nil {
		return &messageSnapshot{Type: "voice", FileID: m.Voice.FileID, Caption: m.Caption}
	}
	if m.VideoNote != nil {
		return &messageSnapshot{Type: "video_note", FileID: m.VideoNote.FileID}
	}
	return nil
}

func getSnapshot(cache *Cache, businessConnectionID string, messageID int) (*messageSnapshot, error) {
	raw, err := cache.GetMessage(businessConnectionID, messageID)
	if err != nil {
		return nil, err
	}

	var snap messageSnapshot
	if err := json.Unmarshal([]byte(raw), &snap); err != nil {
		// Backward compatibility: old format stored plain text or file_id.
		return &messageSnapshot{Type: "text", Text: raw}, nil
	}
	return &snap, nil
}

func sendDeletedSnapshot(ctx context.Context, b *bot.Bot, chatID int64, author string, snap *messageSnapshot) error {
	if snap == nil {
		return nil
	}

	header := fmt.Sprintf("%s удалил(а) сообщение:", author)
	switch snap.Type {
	case "text":
		_, err := b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: chatID,
			Text:   fmt.Sprintf("%s\n\n%s", header, snap.Text),
		})
		return err
	case "photo":
		_, err := b.SendPhoto(ctx, &bot.SendPhotoParams{
			ChatID:  chatID,
			Photo:   &models.InputFileString{Data: snap.FileID},
			Caption: withHeaderCaption(header, snap.Caption),
		})
		return err
	case "video":
		_, err := b.SendVideo(ctx, &bot.SendVideoParams{
			ChatID:  chatID,
			Video:   &models.InputFileString{Data: snap.FileID},
			Caption: withHeaderCaption(header, snap.Caption),
		})
		return err
	case "document":
		_, err := b.SendDocument(ctx, &bot.SendDocumentParams{
			ChatID:   chatID,
			Document: &models.InputFileString{Data: snap.FileID},
			Caption:  withHeaderCaption(header, snap.Caption),
		})
		return err
	case "audio":
		_, err := b.SendAudio(ctx, &bot.SendAudioParams{
			ChatID:  chatID,
			Audio:   &models.InputFileString{Data: snap.FileID},
			Caption: withHeaderCaption(header, snap.Caption),
		})
		return err
	case "voice":
		_, err := b.SendVoice(ctx, &bot.SendVoiceParams{
			ChatID:  chatID,
			Voice:   &models.InputFileString{Data: snap.FileID},
			Caption: withHeaderCaption(header, snap.Caption),
		})
		return err
	case "video_note":
		_, err := b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: chatID,
			Text:   header,
		})
		if err != nil {
			return err
		}
		_, err = b.SendVideoNote(ctx, &bot.SendVideoNoteParams{
			ChatID:    chatID,
			VideoNote: &models.InputFileString{Data: snap.FileID},
		})
		return err
	case "sticker":
		_, err := b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: chatID,
			Text:   header,
		})
		if err != nil {
			return err
		}
		_, err = b.SendSticker(ctx, &bot.SendStickerParams{
			ChatID:  chatID,
			Sticker: &models.InputFileString{Data: snap.FileID},
		})
		return err
	default:
		_, err := b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: chatID,
			Text:   header + "\n\n(неподдерживаемый тип сообщения)",
		})
		return err
	}
}

func withHeaderCaption(header, caption string) string {
	if caption == "" {
		return header
	}
	return header + "\n\n" + caption
}

func notifyOnce(cache *Cache, kind, businessConnectionID string, messageID int, fn func()) {
	ok, err := cache.MarkNotificationSent(kind, businessConnectionID, messageID)
	if err != nil {
		log.Printf("failed to mark notification sent (%s): %v", kind, err)
	}
	if err == nil && !ok {
		return
	}
	fn()
}

func markUpdateOnce(cache *Cache, update *models.Update) bool {
	if update == nil {
		return false
	}
	ok, err := cache.MarkUpdateProcessed(update.ID)
	if err != nil {
		log.Printf("failed to mark update processed: %v", err)
		return true
	}
	return ok
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
