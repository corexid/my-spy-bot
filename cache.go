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

func (c *Cache) QueuePendingNotification(userID int64, payload string) error {
	key := fmt.Sprintf("pending:%d", userID)
	if err := c.client.RPush(c.ctx, key, payload).Err(); err != nil {
		return err
	}
	return c.client.Expire(c.ctx, key, 24*time.Hour).Err()
}

func (c *Cache) PopAllPendingNotifications(userID int64) ([]string, error) {
	key := fmt.Sprintf("pending:%d", userID)
	items, err := c.client.LRange(c.ctx, key, 0, -1).Result()
	if err != nil && err != redis.Nil {
		return nil, err
	}
	if len(items) == 0 {
		return nil, nil
	}
	if err := c.client.Del(c.ctx, key).Err(); err != nil {
		return nil, err
	}
	return items, nil
}
