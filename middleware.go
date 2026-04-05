package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

type SubscriptionChecker struct {
	channelID       int64
	channelUsername string
	ownerUserID     int64
}

func NewSubscriptionChecker(channelID int64, channelUsername string) *SubscriptionChecker {
	var ownerUserID int64
	if raw := strings.TrimSpace(os.Getenv("OWNER_USER_ID")); raw != "" {
		if parsed, err := strconv.ParseInt(raw, 10, 64); err == nil {
			ownerUserID = parsed
		}
	}

	return &SubscriptionChecker{
		channelID:       channelID,
		channelUsername: strings.TrimPrefix(strings.TrimSpace(channelUsername), "@"),
		ownerUserID:     ownerUserID,
	}
}

func (s *SubscriptionChecker) ResolveChannel(ctx context.Context, b *bot.Bot) int64 {
	if s.channelID != 0 || s.channelUsername == "" {
		return s.channelID
	}

	chat, err := b.GetChat(ctx, &bot.GetChatParams{ChatID: "@" + s.channelUsername})
	if err != nil {
		log.Printf("warning: failed to resolve CHANNEL_USERNAME @%s: %v", s.channelUsername, err)
		return 0
	}

	s.channelID = chat.ID
	log.Printf("resolved channel @%s to ID %d", s.channelUsername, s.channelID)
	return s.channelID
}

func (s *SubscriptionChecker) Ensure(ctx context.Context, b *bot.Bot, userID int64, chatID int64) bool {
	if userID == 0 {
		return true
	}
	if s.ownerUserID != 0 && userID == s.ownerUserID {
		return true
	}

	chatRef := s.chatRef()
	if chatRef == nil {
		return true
	}

	member, err := b.GetChatMember(ctx, &bot.GetChatMemberParams{
		ChatID: chatRef,
		UserID: userID,
	})
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "chat not found") {
			log.Printf("subscription check skipped: channel not found for CHANNEL_ID=%d CHANNEL_USERNAME=%s", s.channelID, s.channelUsername)
			return true
		}

		log.Printf("subscription check failed: %v", err)
		lowerErr := strings.ToLower(err.Error())
		accessIssue := strings.Contains(lowerErr, "not enough rights") ||
			strings.Contains(lowerErr, "forbidden") ||
			strings.Contains(lowerErr, "bot is not a member") ||
			strings.Contains(lowerErr, "member list is inaccessible")
		if accessIssue {
			_, _ = b.SendMessage(ctx, &bot.SendMessageParams{
				ChatID:      chatID,
				Text:        "Бот не может проверить подписку. Добавьте бота в канал и выдайте права администратора, затем нажмите /start.",
				ReplyMarkup: s.subscribeMarkup(),
			})
			return false
		}
		_, _ = b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID:      chatID,
			Text:        "Не удалось проверить подписку. Попробуйте позже.",
			ReplyMarkup: s.subscribeMarkup(),
		})
		return false
	}

	if member.Type == models.ChatMemberTypeLeft ||
		member.Type == models.ChatMemberTypeBanned ||
		member.Type == models.ChatMemberTypeRestricted {
		_, _ = b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID:      chatID,
			Text:        "Чтобы пользоваться ботом, подпишитесь на канал и нажмите /start.",
			ReplyMarkup: s.subscribeMarkup(),
		})
		return false
	}

	return true
}

func (s *SubscriptionChecker) chatRef() any {
	if s.channelID != 0 {
		return s.channelID
	}
	if s.channelUsername != "" {
		return "@" + s.channelUsername
	}
	return nil
}

func (s *SubscriptionChecker) subscribeMarkup() models.ReplyMarkup {
	if s.channelUsername == "" {
		return nil
	}

	return &models.InlineKeyboardMarkup{
		InlineKeyboard: [][]models.InlineKeyboardButton{
			{
				{
					Text: "Подписаться на канал",
					URL:  fmt.Sprintf("https://t.me/%s", s.channelUsername),
				},
			},
		},
	}
}
