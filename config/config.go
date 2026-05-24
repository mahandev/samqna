package config

import (
	"fmt"
	"os"
	"strconv"
)

type Config struct {
	Port         string
	DatabasePath string
	MediaPath    string
	ExportPath   string

	OpenRouterKey   string
	TurnstileSite   string
	TurnstileSecret string
	AdminToken      string

	TelegramBotToken string
	TelegramChatID   string

	WorkerCount  int
	WhisperBin   string
	WhisperModel string
	FfmpegBin    string

	QualityThreshold int
	MaxIPPerDay      int
	MaxUploadBytes   int64
	RetentionDays    int
}

func Load() (*Config, error) {
	c := &Config{
		Port:             getenv("PORT", "9000"),
		DatabasePath:     getenv("DATABASE_PATH", "./data/samqna.db"),
		MediaPath:        getenv("MEDIA_PATH", "./data/media"),
		ExportPath:       getenv("EXPORT_PATH", "./data/exports"),
		OpenRouterKey:    os.Getenv("OPENROUTER_API_KEY"),
		TurnstileSite:    os.Getenv("TURNSTILE_SITE_KEY"),
		TurnstileSecret:  os.Getenv("TURNSTILE_SECRET"),
		AdminToken:       os.Getenv("ADMIN_TOKEN"),
		TelegramBotToken: os.Getenv("TELEGRAM_BOT_TOKEN"),
		TelegramChatID:   os.Getenv("TELEGRAM_CHAT_ID"),
		WorkerCount:      getenvInt("WORKER_COUNT", 2),
		WhisperBin:       getenv("WHISPER_BIN", "/usr/local/bin/whisper-cli"),
		WhisperModel:     getenv("WHISPER_MODEL_PATH", "/models/ggml-small.en.bin"),
		FfmpegBin:        getenv("FFMPEG_BIN", "/usr/bin/ffmpeg"),
		QualityThreshold: getenvInt("QUALITY_THRESHOLD", 30),
		MaxIPPerDay:      getenvInt("MAX_SUBMISSIONS_PER_IP_PER_DAY", 3),
		MaxUploadBytes:   int64(getenvInt("MAX_UPLOAD_BYTES", 52428800)),
		RetentionDays:    getenvInt("RETENTION_DAYS", 30),
	}
	if c.AdminToken == "" {
		return nil, fmt.Errorf("ADMIN_TOKEN env var is required")
	}
	return c, nil
}

func getenv(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return def
}

func getenvInt(key string, def int) int {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}
