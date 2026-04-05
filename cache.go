package main

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

type Cache struct {
	client *redis.Client
	ctx    context.Context
}

func NewCache(client *redis.Client) *Cache {
	return &Cache{
		client: client,
		ctx:    context.Background(),
	}
}

func (c *Cache) SaveMessage(businessConnectionID string, messageID int, payload string) error {
	key := fmt.Sprintf("msg:%s:%d", businessConnectionID, messageID)
	return c.client.Set(c.ctx, key, payload, 24*time.Hour).Err()
}

func (c *Cache) GetMessage(businessConnectionID string, messageID int) (string, error) {
	key := fmt.Sprintf("msg:%s:%d", businessConnectionID, messageID)
	return c.client.Get(c.ctx, key).Result()
}

func (c *Cache) MarkUpdateProcessed(updateID int64) (bool, error) {
	key := fmt.Sprintf("upd:%d", updateID)
	return c.client.SetNX(c.ctx, key, "1", 24*time.Hour).Result()
}

func (c *Cache) MarkNotificationSent(kind, businessConnectionID string, messageID int) (bool, error) {
	key := fmt.Sprintf("ntf:%s:%s:%d", kind, businessConnectionID, messageID)
	return c.client.SetNX(c.ctx, key, "1", 24*time.Hour).Result()
}

func (c *Cache) Ping() error {
	return c.client.Ping(c.ctx).Err()
}

func (c *Cache) MarkSubscriptionPromptSent(userID int64) (bool, error) {
	key := fmt.Sprintf("sub_prompt:%d", userID)
	return c.client.SetNX(c.ctx, key, "1", 45*time.Second).Result()
}
