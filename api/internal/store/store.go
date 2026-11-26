// Package store contains Postgres queries.
package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/malinanu/youtubeclipping/api/internal/media"
)

var ErrNotFound = errors.New("not found")

type Store struct{ DB *pgxpool.Pool }

func New(db *pgxpool.Pool) *Store { return &Store{DB: db} }

// ---------- Videos ----------

type Video struct {
	ID         string          `json:"id"`
	SourceURL  string          `json:"sourceUrl"`
	Platform   string          `json:"platform"`
	Title      string          `json:"title"`
	Channel    string          `json:"channel"`
	DurationMs int64           `json:"durationMs"`
	Width      int             `json:"width"`
	Height     int             `json:"height"`
	Thumbnail  string          `json:"thumbnail"`
	Status     string          `json:"status"`
	Progress   float32         `json:"progress"`
	Stage      string          `json:"stage"`
	Error      string          `json:"error"`
	ProxyPath  string          `json:"-"`
	AudioPath  string          `json:"-"`
	Meta       json.RawMessage `json:"meta"`
	CreatedAt  time.Time       `json:"createdAt"`
	UpdatedAt  time.Time       `json:"updatedAt"`
	HasProxy   bool            `json:"hasProxy"`
	ClipCount  int             `json:"clipCount"`
}

const videoCols = `id, source_url, platform, title, channel, duration_ms, width, height, thumbnail,
	status, progress, stage, error, proxy_path, audio_path, meta, created_at, updated_at,
	(SELECT count(*) FROM clips c WHERE c.video_id = videos.id)`

func scanVideo(r pgx.Row) (*Video, error) {
	var v Video
	err := r.Scan(&v.ID, &v.SourceURL, &v.Platform, &v.Title, &v.Channel, &v.DurationMs, &v.Width, &v.Height,
		&v.Thumbnail, &v.Status, &v.Progress, &v.Stage, &v.Error, &v.ProxyPath, &v.AudioPath, &v.Meta,
		&v.CreatedAt, &v.UpdatedAt, &v.ClipCount)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	v.HasProxy = v.ProxyPath != ""
	return &v, err
}

func (s *Store) CreateVideo(ctx context.Context, url, platform string) (*Video, error) {
	return scanVideo(s.DB.QueryRow(ctx,
		`INSERT INTO videos (source_url, platform) VALUES ($1,$2) RETURNING `+videoCols, url, platform))
}

func (s *Store) GetVideo(ctx context.Context, id string) (*Video, error) {
	return scanVideo(s.DB.QueryRow(ctx, `SELECT `+videoCols+` FROM videos WHERE id=$1`, id))
}

func (s *Store) ListVideos(ctx context.Context, limit int) ([]*Video, error) {
	rows, err := s.DB.Query(ctx, `SELECT `+videoCols+` FROM videos ORDER BY created_at DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Video{}
	for rows.Next() {
		v, err := scanVideo(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (s *Store) DeleteVideo(ctx context.Context, id string) error {
	_, err := s.DB.Exec(ctx, `DELETE FROM videos WHERE id=$1`, id)
	return err
}

type VideoMeta struct {
	Title      string
	Channel    string
	DurationMs int64
	Width      int
	Height     int
	Thumbnail  string
	Meta       json.RawMessage
}

func (s *Store) SetVideoMeta(ctx context.Context, id string, m VideoMeta) error {
	if len(m.Meta) == 0 {
		m.Meta = json.RawMessage(`{}`)
	}
	_, err := s.DB.Exec(ctx, `UPDATE videos SET title=$2, channel=$3, duration_ms=$4, width=$5, height=$6,
		thumbnail=$7, meta=$8, updated_at=now() WHERE id=$1`,
		id, m.Title, m.Channel, m.DurationMs, m.Width, m.Height, m.Thumbnail, m.Meta)
	return err
}

func (s *Store) SetVideoStatus(ctx context.Context, id, status, stage string, progress float32, errMsg string) error {
	_, err := s.DB.Exec(ctx, `UPDATE videos SET status=$2, stage=$3, progress=$4, error=$5, updated_at=now() WHERE id=$1`,
		id, status, stage, progress, errMsg)
	return err
}

func (s *Store) SetVideoProgress(ctx context.Context, id string, progress float32) error {
	_, err := s.DB.Exec(ctx, `UPDATE videos SET progress=$2, updated_at=now() WHERE id=$1`, id, progress)
	return err
}

func (s *Store) SetVideoPaths(ctx context.Context, id, proxy, audio string) error {
	_, err := s.DB.Exec(ctx, `UPDATE videos SET proxy_path=$2, audio_path=$3, updated_at=now() WHERE id=$1`, id, proxy, audio)
	return err
}

// VideosOlderThan returns videos whose media can be purged.
func (s *Store) VideosOlderThan(ctx context.Context, cutoff time.Time) ([]string, error) {
	rows, err := s.DB.Query(ctx, `SELECT id FROM videos WHERE created_at < $1 AND (proxy_path <> '' OR audio_path <> '')`, cutoff)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// ---------- Transcripts ----------

type Transcript struct {
	VideoID  string          `json:"videoId"`
	Language string          `json:"language"`
	Provider string          `json:"provider"`
	Words    []media.Word    `json:"words"`
	Segments []media.Segment `json:"segments"`
}

func (s *Store) SaveTranscript(ctx context.Context, t *Transcript) error {
	w, _ := json.Marshal(t.Words)
	sg, _ := json.Marshal(t.Segments)
	_, err := s.DB.Exec(ctx, `INSERT INTO transcripts (video_id, language, provider, words, segments)
		VALUES ($1,$2,$3,$4,$5)
		ON CONFLICT (video_id) DO UPDATE SET language=EXCLUDED.language, provider=EXCLUDED.provider,
		words=EXCLUDED.words, segments=EXCLUDED.segments, created_at=now()`,
		t.VideoID, t.Language, t.Provider, w, sg)
	return err
}

func (s *Store) GetTranscript(ctx context.Context, videoID string) (*Transcript, error) {
	t := Transcript{VideoID: videoID}
	var w, sg []byte
	err := s.DB.QueryRow(ctx, `SELECT language, provider, words, segments FROM transcripts WHERE video_id=$1`, videoID).
		Scan(&t.Language, &t.Provider, &w, &sg)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(w, &t.Words); err != nil {
		return nil, err
	}
	if err := json.Unmarshal(sg, &t.Segments); err != nil {
		return nil, err
	}
	return &t, nil
}

// ---------- Caption styles ----------

type CaptionStyle struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Config    json.RawMessage `json:"config"`
	IsDefault bool            `json:"isDefault"`
}

func (s *Store) ListStyles(ctx context.Context) ([]*CaptionStyle, error) {
	rows, err := s.DB.Query(ctx, `SELECT id, name, config, is_default FROM caption_styles ORDER BY is_default DESC, name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*CaptionStyle{}
	for rows.Next() {
		var cs CaptionStyle
		if err := rows.Scan(&cs.ID, &cs.Name, &cs.Config, &cs.IsDefault); err != nil {
			return nil, err
		}
		out = append(out, &cs)
	}
	return out, rows.Err()
}

func (s *Store) GetStyle(ctx context.Context, id string) (*CaptionStyle, error) {
	var cs CaptionStyle
	q := `SELECT id, name, config, is_default FROM caption_styles WHERE id=$1`
	args := []any{id}
	if id == "" {
		q = `SELECT id, name, config, is_default FROM caption_styles ORDER BY is_default DESC, created_at LIMIT 1`
		args = nil
	}
	err := s.DB.QueryRow(ctx, q, args...).Scan(&cs.ID, &cs.Name, &cs.Config, &cs.IsDefault)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &cs, err
}

func (s *Store) UpsertStyle(ctx context.Context, cs *CaptionStyle) (*CaptionStyle, error) {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if cs.IsDefault {
		if _, err := tx.Exec(ctx, `UPDATE caption_styles SET is_default=false`); err != nil {
			return nil, err
		}
	}
	var out CaptionStyle
	if cs.ID == "" {
		err = tx.QueryRow(ctx, `INSERT INTO caption_styles (name, config, is_default) VALUES ($1,$2,$3)
			RETURNING id, name, config, is_default`, cs.Name, cs.Config, cs.IsDefault).
			Scan(&out.ID, &out.Name, &out.Config, &out.IsDefault)
	} else {
		err = tx.QueryRow(ctx, `UPDATE caption_styles SET name=$2, config=$3, is_default=$4 WHERE id=$1
			RETURNING id, name, config, is_default`, cs.ID, cs.Name, cs.Config, cs.IsDefault).
			Scan(&out.ID, &out.Name, &out.Config, &out.IsDefault)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &out, tx.Commit(ctx)
}

func (s *Store) DeleteStyle(ctx context.Context, id string) error {
	_, err := s.DB.Exec(ctx, `DELETE FROM caption_styles WHERE id=$1`, id)
	return err
}

// ---------- Clips ----------

type Clip struct {
	ID              string       `json:"id"`
	VideoID         string       `json:"videoId"`
	StartMs         int64        `json:"startMs"`
	EndMs           int64        `json:"endMs"`
	Title           string       `json:"title"`
	Description     string       `json:"description"`
	Aspect          string       `json:"aspect"`
	CropX           float32      `json:"cropX"`
	CaptionsEnabled bool         `json:"captionsEnabled"`
	CaptionStyleID  *string      `json:"captionStyleId"`
	WordOverrides   []media.Word `json:"wordOverrides"`
	Source          string       `json:"source"`
	AIScore         float32      `json:"aiScore"`
	AIReason        string       `json:"aiReason"`
	Status          string       `json:"status"`
	Progress        float32      `json:"progress"`
	Error           string       `json:"error"`
	OutputPath      string       `json:"-"`
	SRTPath         string       `json:"-"`
	ThumbPath       string       `json:"-"`
	RenderedAt      *time.Time   `json:"renderedAt"`
	CreatedAt       time.Time    `json:"createdAt"`
	UpdatedAt       time.Time    `json:"updatedAt"`
	HasOutput       bool         `json:"hasOutput"`
}

const clipCols = `id, video_id, start_ms, end_ms, title, description, aspect, crop_x, captions_enabled,
	caption_style_id, word_overrides, source, ai_score, ai_reason, status, progress, error,
	output_path, srt_path, thumb_path, rendered_at, created_at, updated_at`

func scanClip(r pgx.Row) (*Clip, error) {
	var c Clip
	var wo []byte
	err := r.Scan(&c.ID, &c.VideoID, &c.StartMs, &c.EndMs, &c.Title, &c.Description, &c.Aspect, &c.CropX,
		&c.CaptionsEnabled, &c.CaptionStyleID, &wo, &c.Source, &c.AIScore, &c.AIReason, &c.Status, &c.Progress,
		&c.Error, &c.OutputPath, &c.SRTPath, &c.ThumbPath, &c.RenderedAt, &c.CreatedAt, &c.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if len(wo) > 0 {
		_ = json.Unmarshal(wo, &c.WordOverrides)
	}
	if c.WordOverrides == nil {
		c.WordOverrides = []media.Word{}
	}
	c.HasOutput = c.OutputPath != ""
	return &c, nil
}

type ClipInput struct {
	StartMs         int64        `json:"startMs"`
	EndMs           int64        `json:"endMs"`
	Title           string       `json:"title"`
	Description     string       `json:"description"`
	Aspect          string       `json:"aspect"`
	CropX           float32      `json:"cropX"`
	CaptionsEnabled bool         `json:"captionsEnabled"`
	CaptionStyleID  *string      `json:"captionStyleId"`
	WordOverrides   []media.Word `json:"wordOverrides"`
	Source          string       `json:"-"`
	AIScore         float32      `json:"-"`
	AIReason        string       `json:"-"`
}

func (s *Store) CreateClip(ctx context.Context, videoID string, in ClipInput) (*Clip, error) {
	wo, _ := json.Marshal(nonNilWords(in.WordOverrides))
	if in.Source == "" {
		in.Source = "manual"
	}
	return scanClip(s.DB.QueryRow(ctx, `INSERT INTO clips (video_id, start_ms, end_ms, title, description, aspect,
		crop_x, captions_enabled, caption_style_id, word_overrides, source, ai_score, ai_reason)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13) RETURNING `+clipCols,
		videoID, in.StartMs, in.EndMs, in.Title, in.Description, in.Aspect, in.CropX, in.CaptionsEnabled,
		in.CaptionStyleID, wo, in.Source, in.AIScore, in.AIReason))
}

func (s *Store) UpdateClip(ctx context.Context, id string, in ClipInput) (*Clip, error) {
	wo, _ := json.Marshal(nonNilWords(in.WordOverrides))
	return scanClip(s.DB.QueryRow(ctx, `UPDATE clips SET start_ms=$2, end_ms=$3, title=$4, description=$5,
		aspect=$6, crop_x=$7, captions_enabled=$8, caption_style_id=$9, word_overrides=$10, updated_at=now()
		WHERE id=$1 RETURNING `+clipCols,
		id, in.StartMs, in.EndMs, in.Title, in.Description, in.Aspect, in.CropX, in.CaptionsEnabled,
		in.CaptionStyleID, wo))
}

func (s *Store) GetClip(ctx context.Context, id string) (*Clip, error) {
	return scanClip(s.DB.QueryRow(ctx, `SELECT `+clipCols+` FROM clips WHERE id=$1`, id))
}

func (s *Store) ListClips(ctx context.Context, videoID string) ([]*Clip, error) {
	rows, err := s.DB.Query(ctx, `SELECT `+clipCols+` FROM clips WHERE video_id=$1 ORDER BY start_ms`, videoID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Clip{}
	for rows.Next() {
		c, err := scanClip(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) DeleteClip(ctx context.Context, id string) error {
	_, err := s.DB.Exec(ctx, `DELETE FROM clips WHERE id=$1`, id)
	return err
}

func (s *Store) DeleteAIDraftClips(ctx context.Context, videoID string) error {
	_, err := s.DB.Exec(ctx, `DELETE FROM clips WHERE video_id=$1 AND source='ai' AND status='draft'`, videoID)
	return err
}

func (s *Store) SetClipStatus(ctx context.Context, id, status string, progress float32, errMsg string) error {
	_, err := s.DB.Exec(ctx, `UPDATE clips SET status=$2, progress=$3, error=$4, updated_at=now() WHERE id=$1`,
		id, status, progress, errMsg)
	return err
}

func (s *Store) SetClipOutputs(ctx context.Context, id, out, srt, thumb string) error {
	_, err := s.DB.Exec(ctx, `UPDATE clips SET output_path=$2, srt_path=$3, thumb_path=$4, status='done',
		progress=1, error='', rendered_at=now(), updated_at=now() WHERE id=$1`, id, out, srt, thumb)
	return err
}

func nonNilWords(w []media.Word) []media.Word {
	if w == nil {
		return []media.Word{}
	}
	return w
}

// ---------- Publications ----------

type Publication struct {
	ID             string          `json:"id"`
	ClipID         string          `json:"clipId"`
	Integrations   json.RawMessage `json:"integrations"`
	Content        string          `json:"content"`
	ScheduledAt    *time.Time      `json:"scheduledAt"`
	Status         string          `json:"status"`
	PostizResponse json.RawMessage `json:"postizResponse"`
	Error          string          `json:"error"`
	CreatedAt      time.Time       `json:"createdAt"`
}

func (s *Store) CreatePublication(ctx context.Context, p *Publication) (*Publication, error) {
	var out Publication
	err := s.DB.QueryRow(ctx, `INSERT INTO publications (clip_id, integrations, content, scheduled_at)
		VALUES ($1,$2,$3,$4) RETURNING id, clip_id, integrations, content, scheduled_at, status, postiz_response, error, created_at`,
		p.ClipID, p.Integrations, p.Content, p.ScheduledAt).
		Scan(&out.ID, &out.ClipID, &out.Integrations, &out.Content, &out.ScheduledAt, &out.Status,
			&out.PostizResponse, &out.Error, &out.CreatedAt)
	return &out, err
}

func (s *Store) GetPublication(ctx context.Context, id string) (*Publication, error) {
	var out Publication
	err := s.DB.QueryRow(ctx, `SELECT id, clip_id, integrations, content, scheduled_at, status, postiz_response, error, created_at
		FROM publications WHERE id=$1`, id).
		Scan(&out.ID, &out.ClipID, &out.Integrations, &out.Content, &out.ScheduledAt, &out.Status,
			&out.PostizResponse, &out.Error, &out.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &out, err
}

func (s *Store) ListPublications(ctx context.Context, clipID string) ([]*Publication, error) {
	rows, err := s.DB.Query(ctx, `SELECT id, clip_id, integrations, content, scheduled_at, status, postiz_response, error, created_at
		FROM publications WHERE clip_id=$1 ORDER BY created_at DESC`, clipID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Publication{}
	for rows.Next() {
		var p Publication
		if err := rows.Scan(&p.ID, &p.ClipID, &p.Integrations, &p.Content, &p.ScheduledAt, &p.Status,
			&p.PostizResponse, &p.Error, &p.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, &p)
	}
	return out, rows.Err()
}

func (s *Store) SetPublicationResult(ctx context.Context, id, status string, resp json.RawMessage, errMsg string) error {
	if len(resp) == 0 {
		resp = json.RawMessage(`{}`)
	}
	_, err := s.DB.Exec(ctx, `UPDATE publications SET status=$2, postiz_response=$3, error=$4 WHERE id=$1`,
		id, status, resp, errMsg)
	return err
}

// ---------- Settings ----------

// Settings are user-editable runtime options. Secrets set here override env.
type Settings struct {
	WhisperModel       string `json:"whisperModel"`       // small|medium|base|large-v3-turbo...
	WhisperLanguage    string `json:"whisperLanguage"`    // auto|en|...
	TranscribeProvider string `json:"transcribeProvider"` // local|openai|groq
	FallbackProvider   string `json:"fallbackProvider"`   // none|openai|groq
	FallbackOverMin    int    `json:"fallbackOverMinutes"`
	AIProvider         string `json:"aiProvider"` // auto|claude|gemini
	AnthropicAPIKey    string `json:"anthropicApiKey,omitempty"`
	ClaudeModel        string `json:"claudeModel"`
	GeminiAPIKey       string `json:"geminiApiKey,omitempty"`
	GeminiModel        string `json:"geminiModel"`
	OpenAIAPIKey       string `json:"openaiApiKey,omitempty"`
	GroqAPIKey         string `json:"groqApiKey,omitempty"`
	PostizURL          string `json:"postizUrl"`
	PostizAPIKey       string `json:"postizApiKey,omitempty"`
	SuggestMinSec      int    `json:"suggestMinSec"`
	SuggestMaxSec      int    `json:"suggestMaxSec"`
	SuggestCount       int    `json:"suggestCount"`
	SuggestInstruction string `json:"suggestInstructions"`
	// AutoSuggest runs AI clip suggestion after transcription; nil means on.
	AutoSuggest *bool `json:"autoSuggest,omitempty"`
}

func (s *Store) GetSettings(ctx context.Context) (*Settings, error) {
	var raw []byte
	if err := s.DB.QueryRow(ctx, `SELECT data FROM settings WHERE id=1`).Scan(&raw); err != nil {
		return nil, err
	}
	st := &Settings{}
	if err := json.Unmarshal(raw, st); err != nil {
		return nil, err
	}
	return st, nil
}

func (s *Store) SaveSettings(ctx context.Context, st *Settings) error {
	raw, _ := json.Marshal(st)
	_, err := s.DB.Exec(ctx, `UPDATE settings SET data=$1, updated_at=now() WHERE id=1`, raw)
	return err
}

// ---------- Uploads ----------

// UploadMeta is stored under videos.meta.upload for platform='upload'.
type UploadMeta struct {
	Filename string `json:"filename"`
	Size     int64  `json:"size"`
	Ext      string `json:"ext"`
}

// CreateUploadVideo inserts a video in status 'uploading'.
func (s *Store) CreateUploadVideo(ctx context.Context, title string, um UploadMeta) (*Video, error) {
	meta, _ := json.Marshal(map[string]any{"upload": um})
	return scanVideo(s.DB.QueryRow(ctx, `INSERT INTO videos (source_url, platform, title, status, stage, meta)
		VALUES ($1, 'upload', $2, 'uploading', 'uploading', $3) RETURNING `+videoCols,
		"upload://"+um.Filename, title, meta))
}

// UploadInfo returns the upload metadata of an upload video.
func (v *Video) UploadInfo() (UploadMeta, bool) {
	var m struct {
		Upload *UploadMeta `json:"upload"`
	}
	if v.Platform != "upload" || json.Unmarshal(v.Meta, &m) != nil || m.Upload == nil {
		return UploadMeta{}, false
	}
	return *m.Upload, true
}

// SourcePath returns the stored relative path of an uploaded source file.
func (v *Video) SourcePath() string {
	var m struct {
		SourcePath string `json:"sourcePath"`
	}
	_ = json.Unmarshal(v.Meta, &m)
	return m.SourcePath
}

// MergeVideoMeta shallow-merges keys into videos.meta.
func (s *Store) MergeVideoMeta(ctx context.Context, id string, patch map[string]any) error {
	b, _ := json.Marshal(patch)
	_, err := s.DB.Exec(ctx, `UPDATE videos SET meta = meta || $2::jsonb, updated_at=now() WHERE id=$1`, id, b)
	return err
}

// StaleUploads returns uploads that never completed since cutoff.
func (s *Store) StaleUploads(ctx context.Context, cutoff time.Time) ([]string, error) {
	rows, err := s.DB.Query(ctx, `SELECT id FROM videos WHERE platform='upload' AND status='uploading' AND updated_at < $1`, cutoff)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
