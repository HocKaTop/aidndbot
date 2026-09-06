package server

import (
	"errors"
	"net/url"
	"os"
	"strings"
)

type Config struct {
	DatabaseURL, BotToken, BotUsername, AppName, AppURL, SessionSecret, OllamaURL, Model, Addr string
	BotEnabled                                                                                 bool
}

func Env(k, d string) string {
	if s := strings.TrimSpace(os.Getenv(k)); s != "" {
		return s
	}
	return d
}
func LoadConfig() (Config, error) {
	c := Config{DatabaseURL: Env("DATABASE_URL", ""), BotToken: Env("TELEGRAM_BOT_TOKEN", ""), BotUsername: strings.TrimPrefix(Env("TELEGRAM_BOT_USERNAME", ""), "@"), AppName: Env("TELEGRAM_APP_NAME", ""), AppURL: Env("APP_BASE_URL", Env("TELEGRAM_WEBAPP_URL", "http://localhost:8080")), SessionSecret: Env("JWT_SECRET", ""), OllamaURL: Env("OLLAMA_URL", "http://host.docker.internal:11434"), Model: Env("DEFAULT_OLLAMA_MODEL", "qwen3:8b"), Addr: Env("HTTP_ADDR", ":8081"), BotEnabled: Env("BOT_ENABLED", "false") == "true"}
	if c.DatabaseURL == "" || c.BotToken == "" || len(c.SessionSecret) < 32 {
		return c, errors.New("DATABASE_URL, TELEGRAM_BOT_TOKEN и JWT_SECRET (минимум 32 символа) обязательны")
	}
	u, e := url.Parse(c.AppURL)
	if e != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
		return c, errors.New("некорректный APP_BASE_URL")
	}
	return c, nil
}
