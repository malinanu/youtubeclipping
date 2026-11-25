// Package config loads runtime configuration from environment variables.
package config

import (
	"encoding/base64"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	HTTPAddr      string
	DatabaseURL   string
	RedisAddr     string
	RedisPassword string
	RedisDB       int

	DataDir  string // root for media: videos/, clips/, tmp/
	FontsDir string

	// Auth (single admin)
	AdminEmail        string
	AdminPasswordHash string // bcrypt
	SessionSecret     []byte
	SessionTTL        time.Duration
	CookieSecure      bool
	AllowedOrigins    []string

	// Tools
	YtDlpBin      string
	FFmpegBin     string
	FFprobeBin    string
	WhisperBin    string
	WhisperModels string // dir with ggml-*.bin
	CookiesFile   string // optional yt-dlp cookies.txt
	YtDlpProxy    string // optional proxy for yt-dlp
	YtDlpPOTURL   string // optional bgutil PO-token provider, e.g. http://bgutil:4416
	YtDlpUpdate   string // self-update command, e.g. "yt-dlp -U" or a pip command
	YtDlpExtra    []string
	ProxyHeight   int // editor proxy height (360)

	// Worker
	RenderConcurrency     int
	TranscribeConcurrency int
	IngestConcurrency     int
	FFmpegThreads         int
	WhisperThreads        int
	MediaRetentionDays    int

	// Integrations (can be overridden by settings table)
	AIProvider      string // auto|claude|gemini
	AnthropicAPIKey string
	ClaudeModel     string
	GeminiAPIKey    string
	GeminiModel     string
	OpenAIAPIKey    string
	GroqAPIKey      string
	PostizURL       string
	PostizAPIKey    string
}

func Load() (*Config, error) {
	c := &Config{
		HTTPAddr:      env("HTTP_ADDR", ":8080"),
		DatabaseURL:   env("DATABASE_URL", "postgres://clipforge:clipforge@localhost:5432/clipforge?sslmode=disable"),
		RedisAddr:     env("REDIS_ADDR", "localhost:6379"),
		RedisPassword: env("REDIS_PASSWORD", ""),
		RedisDB:       envInt("REDIS_DB", 0),

		DataDir:  env("DATA_DIR", "/data"),
		FontsDir: env("FONTS_DIR", ""), // empty = libass uses fontconfig (system + ~/.fonts)

		AdminEmail:        strings.ToLower(env("ADMIN_EMAIL", "")),
		AdminPasswordHash: decodeHash(env("ADMIN_PASSWORD_HASH", "")),
		SessionSecret:     []byte(env("SESSION_SECRET", "")),
		SessionTTL:        time.Duration(envInt("SESSION_TTL_HOURS", 24*14)) * time.Hour,
		CookieSecure:      envBool("COOKIE_SECURE", true),
		AllowedOrigins:    splitCSV(env("ALLOWED_ORIGINS", "")),

		YtDlpBin:      env("YTDLP_BIN", "yt-dlp"),
		FFmpegBin:     env("FFMPEG_BIN", "ffmpeg"),
		FFprobeBin:    env("FFPROBE_BIN", "ffprobe"),
		WhisperBin:    env("WHISPER_BIN", "whisper-cli"),
		WhisperModels: env("WHISPER_MODELS_DIR", "/models"),
		CookiesFile:   env("YTDLP_COOKIES_FILE", ""),
		YtDlpProxy:    env("YTDLP_PROXY", ""),
		YtDlpPOTURL:   env("YTDLP_POT_URL", ""),
		YtDlpUpdate:   env("YTDLP_UPDATE_CMD", "yt-dlp -U"),
		YtDlpExtra:    strings.Fields(env("YTDLP_EXTRA_ARGS", "")),
		ProxyHeight:   envInt("PROXY_HEIGHT", 360),

		RenderConcurrency:     envInt("RENDER_CONCURRENCY", 1),
		TranscribeConcurrency: envInt("TRANSCRIBE_CONCURRENCY", 1),
		IngestConcurrency:     envInt("INGEST_CONCURRENCY", 2),
		FFmpegThreads:         envInt("FFMPEG_THREADS", 0),
		WhisperThreads:        envInt("WHISPER_THREADS", runtime.NumCPU()),
		MediaRetentionDays:    envInt("MEDIA_RETENTION_DAYS", 14),

		AIProvider:      strings.ToLower(env("AI_PROVIDER", "auto")),
		AnthropicAPIKey: env("ANTHROPIC_API_KEY", ""),
		ClaudeModel:     env("CLAUDE_MODEL", "claude-opus-5-5"),
		GeminiAPIKey:    env("GEMINI_API_KEY", ""),
		GeminiModel:     env("GEMINI_MODEL", "gemini-3.5-flash-lite"),
		OpenAIAPIKey:    env("OPENAI_API_KEY", ""),
		GroqAPIKey:      env("GROQ_API_KEY", ""),
		PostizURL:       strings.TrimRight(env("POSTIZ_API_URL", ""), "/"),
		PostizAPIKey:    env("POSTIZ_API_KEY", ""),
	}
	return c, nil
}

// ValidateAPI checks settings required only by the HTTP server.
func (c *Config) ValidateAPI() error {
	if c.AdminEmail == "" || c.AdminPasswordHash == "" {
		return fmt.Errorf("ADMIN_EMAIL and ADMIN_PASSWORD_HASH are required")
	}
	if len(c.SessionSecret) < 32 {
		return fmt.Errorf("SESSION_SECRET must be at least 32 bytes")
	}
	return nil
}

// decodeHash accepts a raw bcrypt hash ("$2a$...") or its base64 encoding.
// The base64 form contains no "$", so it survives env files, Portainer stack
// variables and shells without quoting.
func decodeHash(v string) string {
	v = strings.TrimSpace(v)
	if v == "" || strings.HasPrefix(v, "$2") {
		return v
	}
	if b, err := base64.StdEncoding.DecodeString(v); err == nil && strings.HasPrefix(string(b), "$2") {
		return string(b)
	}
	return v
}

func env(k, def string) string {
	if v, ok := os.LookupEnv(k); ok && v != "" {
		return v
	}
	return def
}

func envInt(k string, def int) int {
	if v := os.Getenv(k); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func envBool(k string, def bool) bool {
	if v := os.Getenv(k); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			return b
		}
	}
	return def
}

func splitCSV(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
