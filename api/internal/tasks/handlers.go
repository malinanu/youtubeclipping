package tasks

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/hibiken/asynq"
	"github.com/redis/go-redis/v9"

	"github.com/malinanu/youtubeclipping/api/internal/captions"
	"github.com/malinanu/youtubeclipping/api/internal/config"
	"github.com/malinanu/youtubeclipping/api/internal/events"
	"github.com/malinanu/youtubeclipping/api/internal/ffmpeg"
	"github.com/malinanu/youtubeclipping/api/internal/llm"
	"github.com/malinanu/youtubeclipping/api/internal/media"
	"github.com/malinanu/youtubeclipping/api/internal/postiz"
	"github.com/malinanu/youtubeclipping/api/internal/store"
	"github.com/malinanu/youtubeclipping/api/internal/whisper"
	"github.com/malinanu/youtubeclipping/api/internal/ytdlp"
)

type Handlers struct {
	Cfg    *config.Config
	Store  *store.Store
	Bus    *events.Bus
	Redis  *redis.Client
	Enq    *Enqueuer
	YtDlp  *ytdlp.Client
	FF     *ffmpeg.Runner
	Layout media.Layout
	Log    *slog.Logger
	// Gate limits heavy CPU/RAM jobs (whisper.cpp, encodes) running at once.
	Gate func(ctx context.Context) (release func(), err error)
}

func (h *Handlers) Register(mux *asynq.ServeMux) {
	mux.HandleFunc(TypeIngest, h.Ingest)
	mux.HandleFunc(TypeTranscribe, h.Transcribe)
	mux.HandleFunc(TypeSuggest, h.Suggest)
	mux.HandleFunc(TypeRender, h.Render)
	mux.HandleFunc(TypePublish, h.Publish)
	mux.HandleFunc(TypeCleanup, h.Cleanup)
	mux.HandleFunc(TypeYtDlpUp, h.UpdateYtDlp)
}

func (h *Handlers) settings(ctx context.Context) Effective {
	s, err := h.Store.GetSettings(ctx)
	if err != nil {
		h.Log.Warn("load settings", "err", err)
	}
	return Resolve(h.Cfg, s)
}

// lastAttempt reports whether a failure now is final (no retries left).
func lastAttempt(ctx context.Context) bool {
	r, ok1 := asynq.GetRetryCount(ctx)
	m, ok2 := asynq.GetMaxRetry(ctx)
	return !ok1 || !ok2 || r >= m
}

// ---------- video status helpers ----------

func (h *Handlers) videoStatus(ctx context.Context, id, status, stage string, p float32, errMsg string) {
	if err := h.Store.SetVideoStatus(ctx, id, status, stage, p, errMsg); err != nil {
		h.Log.Error("set video status", "id", id, "err", err)
	}
	h.Bus.Publish(ctx, events.Event{Kind: "video", ID: id, VideoID: id, Status: status, Stage: stage, Progress: p, Error: errMsg}, true)
}

func (h *Handlers) videoProgress(ctx context.Context, id, status, stage string, p float32) {
	_ = h.Store.SetVideoProgress(ctx, id, p)
	h.Bus.Publish(ctx, events.Event{Kind: "video", ID: id, VideoID: id, Status: status, Stage: stage, Progress: p}, false)
}

func (h *Handlers) failVideo(ctx context.Context, id string, err error) error {
	permanent := ytdlp.IsPermanent(err)
	if permanent || lastAttempt(ctx) {
		h.videoStatus(context.WithoutCancel(ctx), id, "failed", "", 0, friendlyError(err))
	}
	return retryable(err)
}

// retryable wraps permanent fetch errors (bot checks, private/removed videos)
// with asynq.SkipRetry: retrying them only delays the error for the user.
func retryable(err error) error {
	if ytdlp.IsPermanent(err) {
		return fmt.Errorf("%w: %v", asynq.SkipRetry, err)
	}
	return err
}

// friendlyError maps common yt-dlp failures to actionable messages.
func friendlyError(err error) string { return ytdlp.FriendlyError(err) }

func trim(s string, n int) string {
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}

// ---------- ingest ----------

func (h *Handlers) Ingest(ctx context.Context, t *asynq.Task) error {
	var p VideoPayload
	if err := json.Unmarshal(t.Payload(), &p); err != nil {
		return fmt.Errorf("%w: %v", asynq.SkipRetry, err)
	}
	v, err := h.Store.GetVideo(ctx, p.VideoID)
	if errors.Is(err, store.ErrNotFound) {
		return nil // deleted meanwhile
	}
	if err != nil {
		return err
	}
	dir := h.Layout.VideoDir(v.ID)
	if err := h.Layout.Ensure(dir, h.Layout.TmpDir()); err != nil {
		return err
	}
	if v.Platform == "upload" {
		if v.Status == "uploading" {
			return nil // not finished uploading; /complete enqueues ingest
		}
		return h.ingestUpload(ctx, v)
	}

	h.videoStatus(ctx, v.ID, "downloading", "metadata", 0, "")
	info, err := h.YtDlp.Probe(ctx, v.SourceURL, "")
	if err != nil {
		return h.failVideo(ctx, v.ID, err)
	}
	maxMin := 8 * 60
	if info.Duration > float64(maxMin*60) {
		h.videoStatus(ctx, v.ID, "failed", "", 0, fmt.Sprintf("video is longer than %d hours", maxMin/60))
		return fmt.Errorf("%w: too long", asynq.SkipRetry)
	}
	meta, _ := json.Marshal(map[string]any{
		"extractor": info.Extractor, "chapters": info.Chapters, "webpageUrl": info.WebpageURL,
		"description": trim(info.Description, 5000), "ytdlpVersion": h.YtDlp.Version(ctx),
	})
	channel := info.Channel
	if channel == "" {
		channel = info.Uploader
	}
	if err := h.Store.SetVideoMeta(ctx, v.ID, store.VideoMeta{
		Title: info.Title, Channel: channel, DurationMs: int64(info.Duration * 1000),
		Width: info.Width, Height: info.Height, Thumbnail: info.Thumbnail, Meta: meta,
	}); err != nil {
		return err
	}

	h.videoStatus(ctx, v.ID, "downloading", "proxy", 0, "")
	proxy, err := h.YtDlp.DownloadProxy(ctx, v.SourceURL, dir, h.Cfg.ProxyHeight, func(f float64) {
		h.videoProgress(ctx, v.ID, "downloading", "proxy", float32(f))
	})
	if err != nil {
		return h.failVideo(ctx, v.ID, err)
	}
	pr, err := h.FF.Probe(ctx, proxy)
	if err != nil {
		return h.failVideo(ctx, v.ID, err)
	}
	if pr.VideoCodec != "h264" && pr.VideoCodec != "vp9" {
		h.videoStatus(ctx, v.ID, "downloading", "transcoding proxy", 0, "")
		safe := filepath.Join(dir, "proxy_h264.mp4")
		if err := h.FF.TranscodeProxy(ctx, proxy, safe, h.Cfg.ProxyHeight, pr.DurationSec, func(f float64) {
			h.videoProgress(ctx, v.ID, "downloading", "transcoding proxy", float32(f))
		}); err != nil {
			return h.failVideo(ctx, v.ID, err)
		}
		_ = os.Rename(safe, proxy)
	}
	if !pr.HasAudio {
		h.videoStatus(ctx, v.ID, "failed", "", 0, "downloaded media has no audio track")
		return fmt.Errorf("%w: no audio", asynq.SkipRetry)
	}

	h.videoStatus(ctx, v.ID, "downloading", "extracting audio", 0.95, "")
	wav := filepath.Join(dir, "audio.wav")
	if err := h.FF.ExtractAudio(ctx, proxy, wav); err != nil {
		return h.failVideo(ctx, v.ID, err)
	}
	if err := h.Store.SetVideoPaths(ctx, v.ID, h.Layout.Rel(proxy), h.Layout.Rel(wav)); err != nil {
		return err
	}
	if info.Duration == 0 && pr.DurationSec > 0 {
		_ = h.Store.SetVideoMeta(ctx, v.ID, store.VideoMeta{Title: info.Title, Channel: channel,
			DurationMs: int64(pr.DurationSec * 1000), Width: info.Width, Height: info.Height, Thumbnail: info.Thumbnail, Meta: meta})
	}
	h.videoStatus(ctx, v.ID, "transcribing", "queued", 0, "")
	return h.Enq.Transcribe(ctx, v.ID)
}

// ---------- transcribe ----------

func (h *Handlers) provider(eff Effective, durMin float64, videoID string) whisper.Provider {
	tmp := h.Layout.TmpDir()
	local := &whisper.Local{Bin: h.Cfg.WhisperBin, ModelsDir: h.Cfg.WhisperModels, Model: eff.WhisperModel,
		Language: eff.WhisperLanguage, Threads: h.Cfg.WhisperThreads, FF: h.FF, TmpDir: tmp,
		BeamSize: h.Cfg.WhisperBeamSize, BestOf: h.Cfg.WhisperBestOf, ChunkSec: h.Cfg.WhisperChunkSec,
		MaxContext: h.Cfg.WhisperMaxContext,
		WorkDir:    h.whisperWorkDir(videoID), Gate: h.Gate}
	api := func(name string) whisper.Provider {
		switch name {
		case "openai":
			a := whisper.NewOpenAI(eff.OpenAIAPIKey, h.FF, tmp)
			a.Language = eff.WhisperLanguage
			return a
		case "groq":
			a := whisper.NewGroq(eff.GroqAPIKey, h.FF, tmp)
			a.Language = eff.WhisperLanguage
			return a
		}
		return nil
	}
	if p := api(eff.TranscribeProvider); p != nil {
		return p
	}
	fb := api(eff.FallbackProvider)
	if fb == nil {
		return local
	}
	// Long videos go straight to the API when configured.
	if eff.FallbackOverMin > 0 && durMin > float64(eff.FallbackOverMin) {
		return &whisper.WithFallback{Primary: fb, Fallback: local}
	}
	return &whisper.WithFallback{Primary: local, Fallback: fb}
}

func (h *Handlers) Transcribe(ctx context.Context, t *asynq.Task) error {
	var p VideoPayload
	if err := json.Unmarshal(t.Payload(), &p); err != nil {
		return fmt.Errorf("%w: %v", asynq.SkipRetry, err)
	}
	v, err := h.Store.GetVideo(ctx, p.VideoID)
	if errors.Is(err, store.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	wav, ok := h.Layout.Abs(v.AudioPath)
	if !ok {
		h.videoStatus(ctx, v.ID, "failed", "", 0, "audio not available; re-import the video")
		return fmt.Errorf("%w: no audio path", asynq.SkipRetry)
	}
	eff := h.settings(ctx)
	if eff.UsesLocalWhisper() && !whisper.HasModel(h.Cfg.WhisperModels, eff.WhisperModel) {
		stage := "downloading model " + eff.WhisperModel
		h.videoStatus(ctx, v.ID, "transcribing", stage, 0, "")
		err := whisper.EnsureModel(ctx, h.Cfg.WhisperModels, eff.WhisperModel, func(f float64) {
			h.videoProgress(ctx, v.ID, "transcribing", stage, float32(f))
		})
		if errors.Is(err, whisper.ErrConfig) {
			h.videoStatus(ctx, v.ID, "failed", "", 0, err.Error())
			return fmt.Errorf("%w: %v", asynq.SkipRetry, err)
		}
		if err != nil {
			return h.failVideo(ctx, v.ID, err)
		}
	}
	prov := h.provider(eff, float64(v.DurationMs)/60000, v.ID)
	h.videoStatus(ctx, v.ID, "transcribing", prov.Name(), 0, "")
	res, err := prov.Transcribe(ctx, wav, float64(v.DurationMs)/1000, func(f float64) {
		h.videoProgress(ctx, v.ID, "transcribing", prov.Name(), float32(f))
	})
	if errors.Is(err, whisper.ErrConfig) {
		h.videoStatus(ctx, v.ID, "failed", "", 0, err.Error())
		return fmt.Errorf("%w: %v", asynq.SkipRetry, err)
	}
	if err != nil {
		return h.failVideo(ctx, v.ID, err)
	}
	_ = os.RemoveAll(h.whisperWorkDir(v.ID)) // resume data no longer needed
	if err := h.Store.SaveTranscript(ctx, &store.Transcript{
		VideoID: v.ID, Language: res.Language, Provider: res.Provider,
		Words: res.Words, Segments: media.BuildSegments(res.Words),
	}); err != nil {
		return err
	}
	h.videoStatus(ctx, v.ID, "ready", "", 1, "")
	if eff.AutoSuggest && eff.HasAIKey() {
		h.setSuggest(ctx, v.ID, SuggestState{Status: "queued"})
		if err := h.Enq.Suggest(ctx, SuggestPayload{VideoID: v.ID, Replace: true}); err != nil {
			h.Log.Warn("auto-suggest enqueue", "video", v.ID, "err", err)
			h.setSuggest(context.WithoutCancel(ctx), v.ID, SuggestState{Status: "failed", Error: "could not queue AI suggestions"})
		}
	}
	return nil
}

// ---------- suggest ----------

type SuggestState struct {
	Status string `json:"status"` // queued|running|done|failed
	Stage  string `json:"stage,omitempty"`
	Error  string `json:"error,omitempty"`
	Count  int    `json:"count"`
	At     int64  `json:"at"`
}

func SuggestKey(videoID string) string { return "clipforge:suggest:" + videoID }

func (h *Handlers) setSuggest(ctx context.Context, videoID string, st SuggestState) {
	st.At = time.Now().Unix()
	b, _ := json.Marshal(st)
	h.Redis.Set(ctx, SuggestKey(videoID), b, 2*time.Hour)
	h.Bus.Publish(ctx, events.Event{Kind: "suggest", ID: videoID, VideoID: videoID, Status: st.Status, Stage: st.Stage, Error: st.Error}, true)
}

func (h *Handlers) Suggest(ctx context.Context, t *asynq.Task) error {
	var p SuggestPayload
	if err := json.Unmarshal(t.Payload(), &p); err != nil {
		return fmt.Errorf("%w: %v", asynq.SkipRetry, err)
	}
	v, err := h.Store.GetVideo(ctx, p.VideoID)
	if err != nil {
		return nil
	}
	tr, err := h.Store.GetTranscript(ctx, v.ID)
	if err != nil {
		h.setSuggest(ctx, v.ID, SuggestState{Status: "failed", Error: "transcript not available"})
		return nil
	}
	h.setSuggest(ctx, v.ID, SuggestState{Status: "running", Stage: "Scanning the first 10 minutes"})
	eff := h.settings(ctx)
	instr := strings.TrimSpace(strings.Join([]string{eff.SuggestInstruction, p.Instructions}, "\n"))
	provider, key, model := eff.SuggestProvider()
	var stats llm.Stats
	sugg, err := llm.Suggest(ctx, llm.Options{
		Provider: provider, APIKey: key, Model: model, VideoTitle: v.Title,
		MinSec: eff.SuggestMinSec, MaxSec: eff.SuggestMaxSec, Count: eff.SuggestCount, Instructions: instr,
		Stats: &stats,
		OnProgress: func(pr llm.Progress) {
			if pr.Window >= pr.Windows {
				return
			}
			stage := fmt.Sprintf("Scanned %d/%d sections (up to %s) · %d clips found", pr.Window, pr.Windows, fmtClock(pr.ToSec), pr.Found)
			if pr.Skipped > 0 {
				stage += fmt.Sprintf(" · %d skipped (AI busy)", pr.Skipped)
			}
			h.setSuggest(ctx, v.ID, SuggestState{Status: "running", Stage: stage})
		},
	}, tr.Words)
	if err != nil {
		if llm.IsPermanent(err) || lastAttempt(ctx) {
			h.setSuggest(context.WithoutCancel(ctx), v.ID, SuggestState{Status: "failed", Error: trim(err.Error(), 500)})
		}
		if llm.IsPermanent(err) {
			return fmt.Errorf("%w: %v", asynq.SkipRetry, err)
		}
		return err
	}
	if p.Replace {
		if err := h.Store.DeleteAIDraftClips(ctx, v.ID); err != nil {
			return err
		}
	}
	style, _ := h.Store.GetStyle(ctx, "")
	var styleID *string
	if style != nil {
		styleID = &style.ID
	}
	for _, s := range sugg {
		if _, err := h.Store.CreateClip(ctx, v.ID, store.ClipInput{
			StartMs: int64(s.StartSec * 1000), EndMs: int64(s.EndSec * 1000),
			Title: s.Title, Description: s.Hook, Aspect: "9:16", CropX: 0.5, CaptionsEnabled: true,
			CaptionStyleID: styleID, Source: "ai", AIScore: float32(s.Score), AIReason: s.Reason,
		}); err != nil {
			return err
		}
	}
	done := SuggestState{Status: "done", Count: len(sugg)}
	if stats.Skipped > 0 {
		done.Error = fmt.Sprintf("%d of %d sections skipped because the AI provider was busy; run again to retry them", stats.Skipped, stats.Scanned)
	}
	h.setSuggest(ctx, v.ID, done)
	return nil
}

// fmtClock formats seconds as h:mm:ss or m:ss.
func fmtClock(sec float64) string {
	t := int(sec)
	if t >= 3600 {
		return fmt.Sprintf("%d:%02d:%02d", t/3600, t/60%60, t%60)
	}
	return fmt.Sprintf("%d:%02d", t/60, t%60)
}

// ---------- render ----------

func (h *Handlers) clipStatus(ctx context.Context, c *store.Clip, status string, p float32, errMsg string, force bool) {
	if force {
		_ = h.Store.SetClipStatus(ctx, c.ID, status, p, errMsg)
	} else {
		_, _ = h.Store.DB.Exec(ctx, `UPDATE clips SET progress=$2 WHERE id=$1`, c.ID, p)
	}
	h.Bus.Publish(ctx, events.Event{Kind: "clip", ID: c.ID, VideoID: c.VideoID, Status: status, Progress: p, Error: errMsg}, force)
}

func (h *Handlers) Render(ctx context.Context, t *asynq.Task) error {
	var p ClipPayload
	if err := json.Unmarshal(t.Payload(), &p); err != nil {
		return fmt.Errorf("%w: %v", asynq.SkipRetry, err)
	}
	c, err := h.Store.GetClip(ctx, p.ClipID)
	if errors.Is(err, store.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	v, err := h.Store.GetVideo(ctx, c.VideoID)
	if err != nil {
		return err
	}
	fail := func(err error) error {
		permanent := ytdlp.IsPermanent(err)
		if permanent || lastAttempt(ctx) {
			h.clipStatus(context.WithoutCancel(ctx), c, "failed", 0, friendlyError(err), true)
		}
		return retryable(err)
	}
	h.clipStatus(ctx, c, "rendering", 0, "", true)

	work, err := os.MkdirTemp(h.Layout.TmpDir(), "render-")
	if err != nil {
		return fail(err)
	}
	defer os.RemoveAll(work)
	outDir := h.Layout.ClipDir(c.ID)
	if err := h.Layout.Ensure(outDir); err != nil {
		return fail(err)
	}

	// Words for this clip, rebased to 0.
	var base []media.Word
	if tr, err := h.Store.GetTranscript(ctx, v.ID); err == nil {
		base = tr.Words
	}
	words := media.WordsInRange(media.MergeOverrides(base, c.WordOverrides), c.StartMs, c.EndMs)

	w, hgt := ffmpeg.OutputSize(c.Aspect)
	assPath := ""
	if c.CaptionsEnabled && len(words) > 0 {
		var styleID string
		if c.CaptionStyleID != nil {
			styleID = *c.CaptionStyleID
		}
		cs, err := h.Store.GetStyle(ctx, styleID)
		if err != nil {
			cs, _ = h.Store.GetStyle(ctx, "")
		}
		var raw json.RawMessage
		if cs != nil {
			raw = cs.Config
		}
		assPath = filepath.Join(work, "captions.ass")
		if err := os.WriteFile(assPath, []byte(captions.BuildASS(words, captions.ParseStyle(raw), w, hgt)), 0o644); err != nil {
			return fail(err)
		}
	}

	startSec := float64(c.StartMs) / 1000
	durSec := float64(c.EndMs-c.StartMs) / 1000
	tmpOut := filepath.Join(work, "clip.mp4")
	opts := ffmpeg.RenderOptions{StartSec: startSec, DurSec: durSec, Aspect: c.Aspect, CropX: float64(c.CropX),
		ASSPath: assPath, FontsDir: h.Cfg.FontsDir, Output: tmpOut, Loudnorm: true}
	progress := func(base, span float64) func(float64) {
		return func(f float64) { h.clipStatus(ctx, c, "rendering", float32(base+f*span), "", false) }
	}

	// 0) Uploaded files: render straight from the local source.
	if v.Platform == "upload" {
		src, err := h.uploadSource(v)
		if err != nil {
			return fail(fmt.Errorf("%w: %v", asynq.SkipRetry, err))
		}
		opts.Inputs = []ffmpeg.Input{{Path: src}}
		if err := h.FF.Render(ctx, opts, progress(0, 0.95)); err != nil {
			return fail(err)
		}
		if err := h.checkOutput(ctx, tmpOut, durSec); err != nil {
			return fail(err)
		}
		return h.finishRender(ctx, c, tmpOut, outDir, words, durSec, fail)
	}

	// 1) Stream directly from the platform with an accurate seek (single encode).
	streams, _, rerr := h.YtDlp.ResolveStreams(ctx, v.SourceURL)
	if rerr == nil {
		for _, s := range streams {
			opts.Inputs = append(opts.Inputs, ffmpeg.Input{Path: s.URL, Headers: s.Headers})
		}
		rerr = h.FF.Render(ctx, opts, progress(0, 0.95))
		if rerr == nil {
			// ffmpeg can exit 0 with no frames (e.g. a remote input ends early
			// and -shortest cuts everything); treat that as a failed attempt.
			rerr = h.checkOutput(ctx, tmpOut, durSec)
		}
	}
	// 2) Fallback: let yt-dlp download just the section, then encode locally.
	if rerr != nil {
		h.Log.Warn("direct stream render failed, falling back to section download", "clip", c.ID, "err", rerr)
		h.clipStatus(ctx, c, "rendering", 0.02, "", false)
		sec, err := h.YtDlp.DownloadSection(ctx, v.SourceURL, work, startSec, startSec+durSec, progress(0, 0.5))
		if err != nil {
			return fail(fmt.Errorf("stream: %v; section download: %w", rerr, err))
		}
		opts.Inputs = []ffmpeg.Input{{Path: sec}}
		opts.StartSec = 0
		if err := h.FF.Render(ctx, opts, progress(0.5, 0.45)); err != nil {
			return fail(err)
		}
		if err := h.checkOutput(ctx, tmpOut, durSec); err != nil {
			return fail(err)
		}
	}

	return h.finishRender(ctx, c, tmpOut, outDir, words, durSec, fail)
}

// checkOutput rejects a render that ffmpeg reported as successful but that has
// no video or is much shorter than the clip.
func (h *Handlers) checkOutput(ctx context.Context, path string, wantSec float64) error {
	pr, err := h.FF.Probe(ctx, path)
	if err != nil {
		return fmt.Errorf("rendered file is unreadable: %w", err)
	}
	return outputOK(pr, wantSec)
}

func outputOK(pr *ffmpeg.ProbeResult, wantSec float64) error {
	if pr == nil || pr.Width <= 0 || pr.VideoCodec == "" {
		return errors.New("rendered file has no video stream")
	}
	if pr.DurationSec < max(1, 0.8*wantSec) {
		return fmt.Errorf("rendered file is empty or too short (got %.1fs of %.1fs)", pr.DurationSec, wantSec)
	}
	return nil
}

// moveFile renames, falling back to copy across filesystems.
func moveFile(src, dst string) error {
	if err := os.Rename(src, dst); err == nil {
		return nil
	}
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0o644)
}

// ---------- publish ----------

func (h *Handlers) Publish(ctx context.Context, t *asynq.Task) error {
	var p PublishPayload
	if err := json.Unmarshal(t.Payload(), &p); err != nil {
		return fmt.Errorf("%w: %v", asynq.SkipRetry, err)
	}
	pub, err := h.Store.GetPublication(ctx, p.PublicationID)
	if err != nil {
		return nil
	}
	fail := func(err error) error {
		if lastAttempt(ctx) {
			_ = h.Store.SetPublicationResult(context.WithoutCancel(ctx), pub.ID, "failed", nil, trim(err.Error(), 1000))
			h.Bus.Publish(ctx, events.Event{Kind: "publication", ID: pub.ID, Status: "failed", Error: err.Error()}, true)
		}
		return err
	}
	c, err := h.Store.GetClip(ctx, pub.ClipID)
	if err != nil {
		return fail(err)
	}
	mp4, ok := h.Layout.Abs(c.OutputPath)
	if !ok {
		return fail(fmt.Errorf("%w: clip has not been rendered", asynq.SkipRetry))
	}
	eff := h.settings(ctx)
	pc := postiz.New(eff.PostizURL, eff.PostizAPIKey)
	if !pc.Configured() {
		return fail(fmt.Errorf("%w: Postiz URL/API key not configured", asynq.SkipRetry))
	}
	var targets []postiz.Target
	if err := json.Unmarshal(pub.Integrations, &targets); err != nil || len(targets) == 0 {
		return fail(fmt.Errorf("%w: invalid integrations", asynq.SkipRetry))
	}
	m, err := pc.Upload(ctx, mp4)
	if err != nil {
		return fail(err)
	}
	resp, err := pc.CreatePost(ctx, postiz.CreatePostInput{
		Content: pub.Content, Title: c.Title, When: pub.ScheduledAt, Media: m, Targets: targets, Draft: p.Draft,
	})
	if err != nil {
		return fail(err)
	}
	if !json.Valid(resp) {
		resp, _ = json.Marshal(map[string]string{"raw": string(resp)})
	}
	if err := h.Store.SetPublicationResult(ctx, pub.ID, "sent", resp, ""); err != nil {
		return err
	}
	h.Bus.Publish(ctx, events.Event{Kind: "publication", ID: pub.ID, Status: "sent"}, true)
	return nil
}

// ---------- maintenance ----------

// Cleanup purges editor proxies/audio older than the retention window and stale temp dirs.
// Rendered clips are kept.
func (h *Handlers) Cleanup(ctx context.Context, _ *asynq.Task) error {
	if h.Cfg.MediaRetentionDays > 0 {
		cutoff := time.Now().AddDate(0, 0, -h.Cfg.MediaRetentionDays)
		ids, err := h.Store.VideosOlderThan(ctx, cutoff)
		if err != nil {
			return err
		}
		for _, id := range ids {
			if v, err := h.Store.GetVideo(ctx, id); err == nil && v.Platform == "upload" {
				h.purgeUploadDerived(ctx, v)
				continue
			}
			if err := os.RemoveAll(h.Layout.VideoDir(id)); err != nil {
				h.Log.Warn("cleanup", "video", id, "err", err)
				continue
			}
			_ = h.Store.SetVideoPaths(ctx, id, "", "")
			h.Log.Info("purged source media", "video", id)
		}
	}
	// Uploads abandoned mid-way for a day are removed entirely.
	if stale, err := h.Store.StaleUploads(ctx, time.Now().Add(-24*time.Hour)); err == nil {
		for _, id := range stale {
			_ = os.RemoveAll(h.Layout.VideoDir(id))
			_ = h.Store.DeleteVideo(ctx, id)
			h.Log.Info("removed abandoned upload", "video", id)
		}
	}
	entries, _ := os.ReadDir(h.Layout.TmpDir())
	for _, e := range entries {
		info, err := e.Info()
		if err == nil && time.Since(info.ModTime()) > 24*time.Hour {
			_ = os.RemoveAll(filepath.Join(h.Layout.TmpDir(), e.Name()))
		}
	}
	return nil
}

func (h *Handlers) UpdateYtDlp(ctx context.Context, _ *asynq.Task) error {
	out, err := h.YtDlp.Update(ctx)
	h.Log.Info("yt-dlp update", "output", strings.TrimSpace(out), "err", err)
	return nil
}

// PreloadModel downloads the configured whisper model in the background at
// worker start, so the first transcription doesn't wait for it.
func (h *Handlers) PreloadModel(ctx context.Context) {
	eff := h.settings(ctx)
	if !eff.UsesLocalWhisper() || whisper.HasModel(h.Cfg.WhisperModels, eff.WhisperModel) {
		return
	}
	h.Log.Info("downloading whisper model", "model", eff.WhisperModel, "dir", h.Cfg.WhisperModels)
	last := -1
	err := whisper.EnsureModel(ctx, h.Cfg.WhisperModels, eff.WhisperModel, func(f float64) {
		if pct := int(f * 10); pct != last {
			last = pct
			h.Log.Info("whisper model download", "model", eff.WhisperModel, "progress", fmt.Sprintf("%d%%", pct*10))
		}
	})
	if err != nil {
		h.Log.Error("whisper model download failed", "model", eff.WhisperModel, "err", err)
		return
	}
	h.Log.Info("whisper model ready", "model", eff.WhisperModel)
}

// finishRender moves the encode into place and writes the SRT and thumbnail.
func (h *Handlers) finishRender(ctx context.Context, c *store.Clip, tmpOut, outDir string, words []media.Word,
	durSec float64, fail func(error) error) error {
	finalMP4 := filepath.Join(outDir, "clip.mp4")
	if err := moveFile(tmpOut, finalMP4); err != nil {
		return fail(err)
	}
	srtPath := filepath.Join(outDir, "clip.srt")
	if err := os.WriteFile(srtPath, []byte(captions.BuildSRT(words)), 0o644); err != nil {
		return fail(err)
	}
	thumb := filepath.Join(outDir, "thumb.jpg")
	if err := h.FF.Thumbnail(ctx, finalMP4, thumb, min(1.0, durSec/2)); err != nil {
		h.Log.Warn("thumbnail", "err", err)
		thumb = ""
	}
	relThumb := ""
	if thumb != "" {
		relThumb = h.Layout.Rel(thumb)
	}
	if err := h.Store.SetClipOutputs(ctx, c.ID, h.Layout.Rel(finalMP4), h.Layout.Rel(srtPath), relThumb); err != nil {
		return err
	}
	h.clipStatus(ctx, c, "done", 1, "", true)
	return nil
}

// purgeUploadDerived frees the transcription audio (and a transcoded preview)
// of an old upload but keeps the original, which renders still need.
func (h *Handlers) purgeUploadDerived(ctx context.Context, v *store.Video) {
	if v.AudioPath == "" {
		return
	}
	if p, ok := h.Layout.Abs(v.AudioPath); ok {
		_ = os.Remove(p)
	}
	proxy := v.ProxyPath
	if proxy != "" && proxy != v.SourcePath() {
		if p, ok := h.Layout.Abs(proxy); ok {
			_ = os.Remove(p)
		}
		proxy = ""
	}
	_ = h.Store.SetVideoPaths(ctx, v.ID, proxy, "")
	h.Log.Info("purged upload audio/preview, kept original", "video", v.ID)
}

// whisperWorkDir holds per-chunk whisper.cpp results so retries resume.
func (h *Handlers) whisperWorkDir(videoID string) string {
	return filepath.Join(h.Layout.VideoDir(videoID), "whisper")
}
