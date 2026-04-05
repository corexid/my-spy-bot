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
