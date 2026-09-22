// Package config loads service configuration from the environment.
package config

import (
	"log"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Addr        string
	DatabaseURL string // empty => in-memory store (dev/test only)
	RedisURL    string // empty => in-process job queue (not durable)
	JWTSecret   string
	SecretsKey  string // encrypts tenant credentials at rest

	AIProvider    string // anthropic | openai | mock
	AIFallback    string // optional second provider used when the first fails
	AIModel       string
	AnthropicKey  string
	OpenAIKey     string
	OpenAIBaseURL string // any OpenAI-compatible server (OpenAI, Ollama, vLLM, OpenRouter…)
	OpenAIModel   string
	FallbackModel string

	CORSOrigin string // browser origin of the dashboard
	WebURL     string // public dashboard URL, used in billing redirects
	PublicURL  string // public API URL, shown to customers for inbound webhooks

	StripeKey           string
	StripeWebhookSecret string
	StripePrices        map[string]string // plan -> Stripe price ID

	SchedulerInterval time.Duration // 0 disables the scheduler
	Workers           int
	AllowPrivateNet   bool // let tenants target private-network hosts (self-hosted only)
	DevLogChannels    bool // "deliver" unconfigured channels by logging (development only)
	TrustProxy        bool // take the client IP from X-Forwarded-For (only behind a proxy that sets it)
}

func Load() Config {
	c := Config{
		Addr:                env("ADDR", ":8080"),
		DatabaseURL:         os.Getenv("DATABASE_URL"),
		RedisURL:            os.Getenv("REDIS_URL"),
		JWTSecret:           os.Getenv("JWT_SECRET"),
		SecretsKey:          os.Getenv("SECRETS_KEY"),
		AIModel:             env("AI_MODEL", "claude-sonnet-5"),
		AnthropicKey:        os.Getenv("ANTHROPIC_API_KEY"),
		OpenAIKey:           os.Getenv("OPENAI_API_KEY"),
		OpenAIBaseURL:       env("OPENAI_BASE_URL", "https://api.openai.com/v1"),
		OpenAIModel:         os.Getenv("OPENAI_MODEL"),
		FallbackModel:       os.Getenv("AI_FALLBACK_MODEL"),
		CORSOrigin:          os.Getenv("CORS_ORIGIN"),
		WebURL:              env("WEB_URL", env("CORS_ORIGIN", "http://localhost:3000")),
		PublicURL:           env("PUBLIC_URL", "http://localhost:8080"),
		StripeKey:           os.Getenv("STRIPE_SECRET_KEY"),
		StripeWebhookSecret: os.Getenv("STRIPE_WEBHOOK_SECRET"),
		StripePrices: map[string]string{
			"starter": os.Getenv("STRIPE_PRICE_STARTER"), "growth": os.Getenv("STRIPE_PRICE_GROWTH"), "pro": os.Getenv("STRIPE_PRICE_PRO"),
		},
		Workers:         atoi(os.Getenv("WORKERS"), 4),
		AllowPrivateNet: os.Getenv("ALLOW_PRIVATE_NETWORK") == "true",
		DevLogChannels:  os.Getenv("DEV_LOG_CHANNELS") == "true",
		TrustProxy:      os.Getenv("TRUST_PROXY") == "true",
	}
	if d, err := time.ParseDuration(env("SCHEDULER_INTERVAL", "1m")); err == nil {
		c.SchedulerInterval = d
	}
	c.AIProvider = strings.ToLower(os.Getenv("AI_PROVIDER"))
	c.AIFallback = strings.ToLower(os.Getenv("AI_FALLBACK_PROVIDER"))
	if c.AIProvider == "" {
		switch {
		case c.AnthropicKey != "":
			c.AIProvider = "anthropic"
		case c.OpenAIKey != "":
			c.AIProvider = "openai"
		default:
			c.AIProvider = "mock"
		}
	}
	if c.OpenAIModel == "" {
		c.OpenAIModel = c.AIModel
	}
	if c.JWTSecret == "" {
		c.JWTSecret = "dev-insecure-secret-change-me"
		log.Println("WARNING: JWT_SECRET not set; using an insecure development secret")
	}
	return c
}

func atoi(s string, def int) int {
	if n, err := strconv.Atoi(s); err == nil && n > 0 {
		return n
	}
	return def
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
