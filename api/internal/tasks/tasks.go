// Package tasks defines background jobs (asynq) and their handlers.
package tasks

import (
	"context"
	"encoding/json"
	"time"

	"github.com/hibiken/asynq"

	"github.com/malinanu/youtubeclipping/api/internal/config"
	"github.com/malinanu/youtubeclipping/api/internal/store"
)

const (
	TypeIngest     = "video:ingest"
	TypeTranscribe = "video:transcribe"
	TypeSuggest    = "video:suggest"
	TypeRender     = "clip:render"
	TypePublish    = "clip:publish"
	TypeCleanup    = "maint:cleanup"
	TypeYtDlpUp    = "maint:ytdlp-update"

	QueueIngest     = "ingest"
	QueueTranscribe = "transcribe"
	QueueRender     = "render"
	QueueDefault    = "default"
)

type VideoPayload struct {
	VideoID string `json:"videoId"`
}

type SuggestPayload struct {
	VideoID      string `json:"videoId"`
	Replace      bool   `json:"replace"`
	Instructions string `json:"instructions"`
}

type ClipPayload struct {
	ClipID string `json:"clipId"`
}

type PublishPayload struct {
	PublicationID string `json:"publicationId"`
	Draft         bool   `json:"draft"`
}

// Enqueuer is used by the API to schedule work.
type Enqueuer struct{ C *asynq.Client }

func (e *Enqueuer) enqueue(ctx context.Context, typ string, payload any, opts ...asynq.Option) error {
	b, _ := json.Marshal(payload)
	_, err := e.C.EnqueueContext(ctx, asynq.NewTask(typ, b), opts...)
	return err
}

func (e *Enqueuer) Ingest(ctx context.Context, videoID string) error {
	return e.enqueue(ctx, TypeIngest, VideoPayload{videoID}, asynq.Queue(QueueIngest), asynq.MaxRetry(2), asynq.Timeout(4*time.Hour))
}

func (e *Enqueuer) Transcribe(ctx context.Context, videoID string) error {
	return e.enqueue(ctx, TypeTranscribe, VideoPayload{videoID}, asynq.Queue(QueueTranscribe), asynq.MaxRetry(1), asynq.Timeout(8*time.Hour))
}

func (e *Enqueuer) Suggest(ctx context.Context, p SuggestPayload) error {
	return e.enqueue(ctx, TypeSuggest, p, asynq.Queue(QueueDefault), asynq.MaxRetry(1), asynq.Timeout(20*time.Minute))
}

func (e *Enqueuer) Render(ctx context.Context, clipID string) error {
	return e.enqueue(ctx, TypeRender, ClipPayload{clipID}, asynq.Queue(QueueRender), asynq.MaxRetry(2), asynq.Timeout(45*time.Minute))
}

func (e *Enqueuer) Publish(ctx context.Context, p PublishPayload) error {
	return e.enqueue(ctx, TypePublish, p, asynq.Queue(QueueDefault), asynq.MaxRetry(3), asynq.Timeout(30*time.Minute))
}

// Effective merges DB settings over env config.
type Effective struct {
	WhisperModel       string
	WhisperLanguage    string
	TranscribeProvider string
	FallbackProvider   string
	FallbackOverMin    int
	AnthropicAPIKey    string
	ClaudeModel        string
	OpenAIAPIKey       string
	GroqAPIKey         string
	PostizURL          string
	PostizAPIKey       string
	SuggestMinSec      int
	SuggestMaxSec      int
	SuggestCount       int
	SuggestInstruction string
}

func Resolve(cfg *config.Config, s *store.Settings) Effective {
	pick := func(a, b string) string {
		if a != "" {
			return a
		}
		return b
	}
	pickInt := func(a, b int) int {
		if a > 0 {
			return a
		}
		return b
	}
	if s == nil {
		s = &store.Settings{}
	}
	return Effective{
		WhisperModel:       pick(s.WhisperModel, "small"),
		WhisperLanguage:    pick(s.WhisperLanguage, "auto"),
		TranscribeProvider: pick(s.TranscribeProvider, "local"),
		FallbackProvider:   pick(s.FallbackProvider, "none"),
		FallbackOverMin:    s.FallbackOverMin,
		AnthropicAPIKey:    pick(s.AnthropicAPIKey, cfg.AnthropicAPIKey),
		ClaudeModel:        pick(s.ClaudeModel, cfg.ClaudeModel),
		OpenAIAPIKey:       pick(s.OpenAIAPIKey, cfg.OpenAIAPIKey),
		GroqAPIKey:         pick(s.GroqAPIKey, cfg.GroqAPIKey),
		PostizURL:          pick(s.PostizURL, cfg.PostizURL),
		PostizAPIKey:       pick(s.PostizAPIKey, cfg.PostizAPIKey),
		SuggestMinSec:      pickInt(s.SuggestMinSec, 20),
		SuggestMaxSec:      pickInt(s.SuggestMaxSec, 75),
		SuggestCount:       pickInt(s.SuggestCount, 8),
		SuggestInstruction: s.SuggestInstruction,
	}
}
