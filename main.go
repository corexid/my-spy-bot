package main

import (
	"context"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"github.com/joho/godotenv"
	"github.com/redis/go-redis/v9"
)

func main() {
	if err := godotenv.Load(); err != nil {
		if !os.IsNotExist(err) {
			log.Printf("Warning: failed to load .env: %v", err)
		}
	}

	token := os.Getenv("BOT_TOKEN")
	redisURL := os.Getenv("REDIS_URL")
	channelIDStr := strings.TrimSpace(os.Getenv("CHANNEL_ID"))
	channelUsername := strings.TrimSpace(os.Getenv("CHANNEL_USERNAME"))

	if token == "" || redisURL == "" {
		log.Fatal("BOT_TOKEN and REDIS_URL environment variables are required")
	}

	var channelID int64
	if channelIDStr != "" {
		parsedID, err := strconv.ParseInt(channelIDStr, 10, 64)
		if err != nil {
			log.Fatalf("invalid CHANNEL_ID: %v", err)
		}
		channelID = parsedID
	}

	options, err := redis.ParseURL(redisURL)
	if err != nil {
		log.Fatalf("invalid REDIS_URL: %v", err)
	}

	redisClient := redis.NewClient(options)
	if err := redisClient.Ping(context.Background()).Err(); err != nil {
		log.Fatalf("redis ping failed: %v", err)
	}

	cache := NewCache(redisClient)
	checker := NewSubscriptionChecker(channelID, channelUsername)
	httpClient, usingProxy := newTelegramHTTPClient()
	if usingProxy {
		log.Println("Using proxy for Telegram API requests")
	}

	b, err := bot.New(token,
		bot.WithHTTPClient(10*time.Second, httpClient),
		bot.WithAllowedUpdates(bot.AllowedUpdates{
			models.AllowedUpdateMessage,
			models.AllowedUpdateBusinessConnection,
			models.AllowedUpdateBusinessMessage,
			models.AllowedUpdateEditedBusinessMessage,
			models.AllowedUpdateDeletedBusinessMessages,
		}),
		bot.WithDefaultHandler(func(ctx context.Context, b *bot.Bot, update *models.Update) {
			DefaultHandler(ctx, b, update, cache, checker)
		}),
	)
	if err != nil {
		log.Fatalf("failed to create bot: %v", err)
	}

	// Resolve channel once at startup if CHANNEL_USERNAME is used.
	checker.ResolveChannel(context.Background(), b)
	RegisterHandlers(b, cache, checker)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	log.Println("Bot started")
	b.Start(ctx)
}

func newTelegramHTTPClient() (*http.Client, bool) {
	proxyRaw := strings.TrimSpace(os.Getenv("HTTP_PROXY_URL"))
	if proxyRaw == "" {
		proxyRaw = strings.TrimSpace(os.Getenv("HTTPS_PROXY_URL"))
	}

	proxyFn := http.ProxyFromEnvironment
	usingProxy := false
	if proxyRaw != "" {
		proxyURL, err := url.Parse(proxyRaw)
		if err != nil {
			log.Fatalf("invalid proxy URL in HTTP_PROXY_URL/HTTPS_PROXY_URL: %v", err)
		}
		proxyFn = http.ProxyURL(proxyURL)
		usingProxy = true
	}

	dialer := &net.Dialer{
		Timeout:   12 * time.Second,
		KeepAlive: 30 * time.Second,
	}

	transport := &http.Transport{
		Proxy: proxyFn,
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			return dialer.DialContext(ctx, "tcp4", address)
		},
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   12 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}

	return &http.Client{
		Transport: transport,
		Timeout:   70 * time.Second,
	}, usingProxy
}
