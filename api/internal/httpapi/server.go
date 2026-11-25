// Package httpapi exposes the REST + SSE API consumed by the web UI.
package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"

	"github.com/malinanu/youtubeclipping/api/internal/auth"
	"github.com/malinanu/youtubeclipping/api/internal/config"
	"github.com/malinanu/youtubeclipping/api/internal/events"
	"github.com/malinanu/youtubeclipping/api/internal/media"
	"github.com/malinanu/youtubeclipping/api/internal/postiz"
	"github.com/malinanu/youtubeclipping/api/internal/store"
	"github.com/malinanu/youtubeclipping/api/internal/tasks"
	"github.com/malinanu/youtubeclipping/api/internal/ytdlp"
)

type Server struct {
	Cfg    *config.Config
	Store  *store.Store
	Auth   *auth.Auth
	Enq    *tasks.Enqueuer
	Bus    *events.Bus
	Redis  *redis.Client
	YtDlp  *ytdlp.Client
	Layout media.Layout
	Log    *slog.Logger
}

var uuidRe = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

func (s *Server) Router() *gin.Engine {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery(), s.logger())
	r.MaxMultipartMemory = 2 << 20
	_ = r.SetTrustedProxies([]string{"127.0.0.1", "10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16"})

	r.GET("/healthz", func(c *gin.Context) { c.JSON(200, gin.H{"ok": true}) })

	api := r.Group("/api")
	api.POST("/auth/login", s.login)
	api.POST("/auth/logout", s.logout)

	p := api.Group("", s.Auth.Middleware())
	p.GET("/auth/me", func(c *gin.Context) { c.JSON(200, gin.H{"email": s.Cfg.AdminEmail}) })
	p.GET("/events", s.sse)
	p.GET("/system", s.system)

	p.GET("/videos", s.listVideos)
	p.POST("/videos", s.createVideo)
	p.GET("/videos/:id", s.withUUID(s.getVideo))
	p.DELETE("/videos/:id", s.withUUID(s.deleteVideo))
	p.POST("/videos/:id/retry", s.withUUID(s.retryVideo))
	p.POST("/videos/:id/transcribe", s.withUUID(s.retranscribe))
	p.GET("/videos/:id/transcript", s.withUUID(s.getTranscript))
	p.GET("/videos/:id/proxy", s.withUUID(s.serveProxy))
	p.GET("/videos/:id/clips", s.withUUID(s.listClips))
	p.POST("/videos/:id/clips", s.withUUID(s.createClip))
	p.POST("/videos/:id/suggest", s.withUUID(s.suggest))
	p.GET("/videos/:id/suggest", s.withUUID(s.suggestStatus))

	p.GET("/clips/:id", s.withUUID(s.getClip))
	p.PUT("/clips/:id", s.withUUID(s.updateClip))
	p.DELETE("/clips/:id", s.withUUID(s.deleteClip))
	p.POST("/clips/:id/render", s.withUUID(s.renderClip))
	p.GET("/clips/:id/file/:kind", s.withUUID(s.serveClipFile))
	p.GET("/clips/:id/publications", s.withUUID(s.listPublications))
	p.POST("/clips/:id/publish", s.withUUID(s.publishClip))

	p.GET("/styles", s.listStyles)
	p.POST("/styles", s.saveStyle)
	p.PUT("/styles/:id", s.withUUID(s.saveStyle))
	p.DELETE("/styles/:id", s.withUUID(s.deleteStyle))

	p.GET("/settings", s.getSettings)
	p.PUT("/settings", s.putSettings)
	p.POST("/settings/cookies", s.uploadCookies)
	p.DELETE("/settings/cookies", s.deleteCookies)
	p.GET("/postiz/integrations", s.postizIntegrations)
	return r
}

func (s *Server) logger() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		if c.Request.URL.Path == "/healthz" || c.Request.URL.Path == "/api/events" {
			return
		}
		s.Log.Info("http", "method", c.Request.Method, "path", c.Request.URL.Path, "status", c.Writer.Status(),
			"ms", time.Since(start).Milliseconds())
	}
}

func (s *Server) withUUID(h gin.HandlerFunc) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !uuidRe.MatchString(c.Param("id")) {
			c.JSON(400, gin.H{"error": "invalid id"})
			return
		}
		h(c)
	}
}

func fail(c *gin.Context, err error) {
	if errors.Is(err, store.ErrNotFound) {
		c.JSON(404, gin.H{"error": "not found"})
		return
	}
	c.JSON(500, gin.H{"error": err.Error()})
}

func bad(c *gin.Context, msg string) { c.JSON(400, gin.H{"error": msg}) }

// ---------- auth ----------

func (s *Server) login(c *gin.Context) {
	if !s.Auth.Allow(c.ClientIP()) {
		c.JSON(429, gin.H{"error": "too many attempts, try again shortly"})
		return
	}
	var in struct{ Email, Password string }
	if err := c.ShouldBindJSON(&in); err != nil {
		bad(c, "invalid body")
		return
	}
	if !s.Auth.CheckCredentials(in.Email, in.Password) {
		c.JSON(401, gin.H{"error": "invalid email or password"})
		return
	}
	s.Auth.SetCookie(c)
	c.JSON(200, gin.H{"email": s.Cfg.AdminEmail})
}

func (s *Server) logout(c *gin.Context) {
	s.Auth.ClearCookie(c)
	c.JSON(200, gin.H{"ok": true})
}

// ---------- SSE ----------

func (s *Server) sse(c *gin.Context) {
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no") // nginx/CloudPanel: disable buffering
	ctx := c.Request.Context()
	ch := s.Bus.Subscribe(ctx)
	ping := time.NewTicker(20 * time.Second)
	defer ping.Stop()
	c.Writer.WriteHeader(200)
	_, _ = io.WriteString(c.Writer, "retry: 3000\n\n")
	c.Writer.Flush()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ping.C:
			_, _ = io.WriteString(c.Writer, ": ping\n\n")
			c.Writer.Flush()
		case e, ok := <-ch:
			if !ok {
				return
			}
			b, _ := json.Marshal(e)
			_, _ = fmt.Fprintf(c.Writer, "data: %s\n\n", b)
			c.Writer.Flush()
		}
	}
}

func (s *Server) system(c *gin.Context) {
	models, _ := filepath.Glob(filepath.Join(s.Cfg.WhisperModels, "ggml-*.bin"))
	names := []string{}
	for _, m := range models {
		names = append(names, strings.TrimSuffix(strings.TrimPrefix(filepath.Base(m), "ggml-"), ".bin"))
	}
	_, cookieErr := os.Stat(s.Cfg.CookiesFile)
	c.JSON(200, gin.H{
		"ytdlpVersion":  s.YtDlp.Version(c.Request.Context()),
		"whisperModels": names,
		"cookiesSet":    s.Cfg.CookiesFile != "" && cookieErr == nil,
	})
}

// ---------- videos ----------

func (s *Server) listVideos(c *gin.Context) {
	vs, err := s.Store.ListVideos(c.Request.Context(), 200)
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(200, vs)
}

func (s *Server) createVideo(c *gin.Context) {
	var in struct {
		URL string `json:"url"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		bad(c, "invalid body")
		return
	}
	norm, platform, err := ytdlp.ValidateURL(in.URL)
	if err != nil {
		bad(c, err.Error())
		return
	}
	ctx := c.Request.Context()
	v, err := s.Store.CreateVideo(ctx, norm, platform)
	if err != nil {
		fail(c, err)
		return
	}
	if err := s.Enq.Ingest(ctx, v.ID); err != nil {
		// Don't leave the video stuck in "queued": mark it failed so Retry is offered.
		_ = s.Store.SetVideoStatus(context.WithoutCancel(ctx), v.ID, "failed", "", 0, "could not queue job (is Redis reachable?): "+err.Error())
		fail(c, err)
		return
	}
	c.JSON(201, v)
}

func (s *Server) getVideo(c *gin.Context) {
	v, err := s.Store.GetVideo(c.Request.Context(), c.Param("id"))
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(200, v)
}

func (s *Server) deleteVideo(c *gin.Context) {
	id := c.Param("id")
	ctx := c.Request.Context()
	clips, _ := s.Store.ListClips(ctx, id)
	if err := s.Store.DeleteVideo(ctx, id); err != nil {
		fail(c, err)
		return
	}
	_ = os.RemoveAll(s.Layout.VideoDir(id))
	for _, cl := range clips {
		_ = os.RemoveAll(s.Layout.ClipDir(cl.ID))
	}
	c.Status(204)
}

func (s *Server) retryVideo(c *gin.Context) {
	ctx := c.Request.Context()
	v, err := s.Store.GetVideo(ctx, c.Param("id"))
	if err != nil {
		fail(c, err)
		return
	}
	if v.AudioPath != "" {
		if abs, ok := s.Layout.Abs(v.AudioPath); ok {
			if _, err := os.Stat(abs); err == nil {
				_ = s.Store.SetVideoStatus(ctx, v.ID, "transcribing", "queued", 0, "")
				if err := s.Enq.Transcribe(ctx, v.ID); err != nil {
					_ = s.Store.SetVideoStatus(context.WithoutCancel(ctx), v.ID, "failed", "", 0, "could not queue job: "+err.Error())
					fail(c, err)
					return
				}
				c.JSON(202, gin.H{"queued": "transcribe"})
				return
			}
		}
	}
	_ = s.Store.SetVideoStatus(ctx, v.ID, "queued", "", 0, "")
	if err := s.Enq.Ingest(ctx, v.ID); err != nil {
		_ = s.Store.SetVideoStatus(context.WithoutCancel(ctx), v.ID, "failed", "", 0, "could not queue job: "+err.Error())
		fail(c, err)
		return
	}
	c.JSON(202, gin.H{"queued": "ingest"})
}

func (s *Server) retranscribe(c *gin.Context) {
	ctx := c.Request.Context()
	v, err := s.Store.GetVideo(ctx, c.Param("id"))
	if err != nil {
		fail(c, err)
		return
	}
	if v.AudioPath == "" {
		bad(c, "source audio was purged; use retry to re-import")
		return
	}
	_ = s.Store.SetVideoStatus(ctx, v.ID, "transcribing", "queued", 0, "")
	if err := s.Enq.Transcribe(ctx, v.ID); err != nil {
		_ = s.Store.SetVideoStatus(context.WithoutCancel(ctx), v.ID, "failed", "", 0, "could not queue job: "+err.Error())
		fail(c, err)
		return
	}
	c.JSON(202, gin.H{"queued": true})
}

func (s *Server) getTranscript(c *gin.Context) {
	t, err := s.Store.GetTranscript(c.Request.Context(), c.Param("id"))
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(200, t)
}

func (s *Server) serveProxy(c *gin.Context) {
	v, err := s.Store.GetVideo(c.Request.Context(), c.Param("id"))
	if err != nil {
		fail(c, err)
		return
	}
	p, ok := s.Layout.Abs(v.ProxyPath)
	if !ok {
		c.JSON(404, gin.H{"error": "proxy not available"})
		return
	}
	c.Header("Cache-Control", "private, max-age=86400")
	c.File(p) // http.ServeContent: supports Range requests for seeking
}

// ---------- clips ----------

func (s *Server) listClips(c *gin.Context) {
	cl, err := s.Store.ListClips(c.Request.Context(), c.Param("id"))
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(200, cl)
}

func validateClip(in *store.ClipInput, durMs int64) error {
	if in.StartMs < 0 || in.EndMs <= in.StartMs {
		return errors.New("end must be after start")
	}
	if durMs > 0 && in.EndMs > durMs+1000 {
		return errors.New("clip ends after the video")
	}
	if in.EndMs-in.StartMs > 15*60*1000 {
		return errors.New("clips are limited to 15 minutes")
	}
	if in.EndMs-in.StartMs < 1000 {
		return errors.New("clips must be at least 1 second")
	}
	switch in.Aspect {
	case "9:16", "16:9", "1:1":
	case "":
		in.Aspect = "9:16"
	default:
		return errors.New("invalid aspect")
	}
	if in.CropX < 0 || in.CropX > 1 {
		in.CropX = 0.5
	}
	if in.CaptionStyleID != nil && !uuidRe.MatchString(*in.CaptionStyleID) {
		in.CaptionStyleID = nil
	}
	if len(in.Title) > 300 {
		in.Title = in.Title[:300]
	}
	if len(in.WordOverrides) > 20000 {
		return errors.New("too many word overrides")
	}
	return nil
}

func (s *Server) createClip(c *gin.Context) {
	ctx := c.Request.Context()
	v, err := s.Store.GetVideo(ctx, c.Param("id"))
	if err != nil {
		fail(c, err)
		return
	}
	in := store.ClipInput{Aspect: "9:16", CropX: 0.5, CaptionsEnabled: true}
	if err := c.ShouldBindJSON(&in); err != nil {
		bad(c, "invalid body")
		return
	}
	if err := validateClip(&in, v.DurationMs); err != nil {
		bad(c, err.Error())
		return
	}
	if in.CaptionStyleID == nil {
		if st, err := s.Store.GetStyle(ctx, ""); err == nil {
			in.CaptionStyleID = &st.ID
		}
	}
	cl, err := s.Store.CreateClip(ctx, v.ID, in)
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(201, cl)
}

func (s *Server) getClip(c *gin.Context) {
	cl, err := s.Store.GetClip(c.Request.Context(), c.Param("id"))
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(200, cl)
}

func (s *Server) updateClip(c *gin.Context) {
	ctx := c.Request.Context()
	cur, err := s.Store.GetClip(ctx, c.Param("id"))
	if err != nil {
		fail(c, err)
		return
	}
	v, err := s.Store.GetVideo(ctx, cur.VideoID)
	if err != nil {
		fail(c, err)
		return
	}
	in := store.ClipInput{StartMs: cur.StartMs, EndMs: cur.EndMs, Title: cur.Title, Description: cur.Description,
		Aspect: cur.Aspect, CropX: cur.CropX, CaptionsEnabled: cur.CaptionsEnabled, CaptionStyleID: cur.CaptionStyleID,
		WordOverrides: cur.WordOverrides}
	if err := c.ShouldBindJSON(&in); err != nil {
		bad(c, "invalid body")
		return
	}
	if err := validateClip(&in, v.DurationMs); err != nil {
		bad(c, err.Error())
		return
	}
	cl, err := s.Store.UpdateClip(ctx, cur.ID, in)
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(200, cl)
}

func (s *Server) deleteClip(c *gin.Context) {
	id := c.Param("id")
	if err := s.Store.DeleteClip(c.Request.Context(), id); err != nil {
		fail(c, err)
		return
	}
	_ = os.RemoveAll(s.Layout.ClipDir(id))
	c.Status(204)
}

func (s *Server) renderClip(c *gin.Context) {
	ctx := c.Request.Context()
	cl, err := s.Store.GetClip(ctx, c.Param("id"))
	if err != nil {
		fail(c, err)
		return
	}
	if cl.Status == "queued" || cl.Status == "rendering" {
		c.JSON(409, gin.H{"error": "clip is already rendering"})
		return
	}
	if err := s.Store.SetClipStatus(ctx, cl.ID, "queued", 0, ""); err != nil {
		fail(c, err)
		return
	}
	if err := s.Enq.Render(ctx, cl.ID); err != nil {
		_ = s.Store.SetClipStatus(ctx, cl.ID, "failed", 0, err.Error())
		fail(c, err)
		return
	}
	s.Bus.Publish(ctx, events.Event{Kind: "clip", ID: cl.ID, VideoID: cl.VideoID, Status: "queued"}, true)
	c.JSON(202, gin.H{"queued": true})
}

func (s *Server) serveClipFile(c *gin.Context) {
	cl, err := s.Store.GetClip(c.Request.Context(), c.Param("id"))
	if err != nil {
		fail(c, err)
		return
	}
	var rel, name, ctype string
	base := safeName(cl.Title)
	switch c.Param("kind") {
	case "mp4":
		rel, name, ctype = cl.OutputPath, base+".mp4", "video/mp4"
	case "srt":
		rel, name, ctype = cl.SRTPath, base+".srt", "application/x-subrip"
	case "thumb":
		rel, name, ctype = cl.ThumbPath, base+".jpg", "image/jpeg"
	default:
		bad(c, "unknown file kind")
		return
	}
	p, ok := s.Layout.Abs(rel)
	if !ok {
		c.JSON(404, gin.H{"error": "file not available"})
		return
	}
	c.Header("Content-Type", ctype)
	c.Header("Cache-Control", "private, no-cache")
	if c.Query("download") == "1" {
		c.FileAttachment(p, name)
		return
	}
	c.File(p)
}

var unsafeRe = regexp.MustCompile(`[^A-Za-z0-9._ -]+`)

func safeName(s string) string {
	s = strings.TrimSpace(unsafeRe.ReplaceAllString(s, ""))
	if s == "" {
		return "clip"
	}
	if len(s) > 80 {
		s = s[:80]
	}
	return s
}

// ---------- suggestions ----------

func (s *Server) suggest(c *gin.Context) {
	ctx := c.Request.Context()
	id := c.Param("id")
	if _, err := s.Store.GetTranscript(ctx, id); err != nil {
		bad(c, "transcript not ready yet")
		return
	}
	var in struct {
		Replace      bool   `json:"replace"`
		Instructions string `json:"instructions"`
	}
	_ = c.ShouldBindJSON(&in)
	if len(in.Instructions) > 2000 {
		in.Instructions = in.Instructions[:2000]
	}
	st, _ := json.Marshal(tasks.SuggestState{Status: "queued", At: time.Now().Unix()})
	s.Redis.Set(ctx, tasks.SuggestKey(id), st, 2*time.Hour)
	if err := s.Enq.Suggest(ctx, tasks.SuggestPayload{VideoID: id, Replace: in.Replace, Instructions: in.Instructions}); err != nil {
		fail(c, err)
		return
	}
	c.JSON(202, gin.H{"status": "queued"})
}

func (s *Server) suggestStatus(c *gin.Context) {
	raw, err := s.Redis.Get(c.Request.Context(), tasks.SuggestKey(c.Param("id"))).Bytes()
	if err != nil {
		c.JSON(200, gin.H{"status": "idle"})
		return
	}
	c.Data(200, "application/json", raw)
}

// ---------- publishing ----------

func (s *Server) listPublications(c *gin.Context) {
	ps, err := s.Store.ListPublications(c.Request.Context(), c.Param("id"))
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(200, ps)
}

func (s *Server) publishClip(c *gin.Context) {
	ctx := c.Request.Context()
	cl, err := s.Store.GetClip(ctx, c.Param("id"))
	if err != nil {
		fail(c, err)
		return
	}
	if !cl.HasOutput {
		bad(c, "render the clip first")
		return
	}
	var in struct {
		Targets     []postiz.Target `json:"targets"`
		Content     string          `json:"content"`
		ScheduledAt *time.Time      `json:"scheduledAt"`
		Draft       bool            `json:"draft"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		bad(c, "invalid body")
		return
	}
	if len(in.Targets) == 0 {
		bad(c, "select at least one channel")
		return
	}
	for _, t := range in.Targets {
		if t.IntegrationID == "" || t.Identifier == "" {
			bad(c, "each target needs integrationId and identifier")
			return
		}
	}
	targets, _ := json.Marshal(in.Targets)
	pub, err := s.Store.CreatePublication(ctx, &store.Publication{
		ClipID: cl.ID, Integrations: targets, Content: in.Content, ScheduledAt: in.ScheduledAt,
	})
	if err != nil {
		fail(c, err)
		return
	}
	if err := s.Enq.Publish(ctx, tasks.PublishPayload{PublicationID: pub.ID, Draft: in.Draft}); err != nil {
		fail(c, err)
		return
	}
	c.JSON(202, pub)
}

func (s *Server) effective(ctx context.Context) tasks.Effective {
	st, _ := s.Store.GetSettings(ctx)
	return tasks.Resolve(s.Cfg, st)
}

func (s *Server) postizIntegrations(c *gin.Context) {
	eff := s.effective(c.Request.Context())
	pc := postiz.New(eff.PostizURL, eff.PostizAPIKey)
	if !pc.Configured() {
		c.JSON(200, gin.H{"configured": false, "integrations": []any{}})
		return
	}
	list, err := pc.Integrations(c.Request.Context())
	if err != nil {
		c.JSON(502, gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, gin.H{"configured": true, "integrations": list})
}

// ---------- styles ----------

func (s *Server) listStyles(c *gin.Context) {
	st, err := s.Store.ListStyles(c.Request.Context())
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(200, st)
}

func (s *Server) saveStyle(c *gin.Context) {
	var in store.CaptionStyle
	if err := c.ShouldBindJSON(&in); err != nil {
		bad(c, "invalid body")
		return
	}
	in.ID = c.Param("id")
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || !json.Valid(in.Config) {
		bad(c, "name and config are required")
		return
	}
	out, err := s.Store.UpsertStyle(c.Request.Context(), &in)
	if err != nil {
		if strings.Contains(err.Error(), "duplicate key") {
			bad(c, "a style with that name exists")
			return
		}
		fail(c, err)
		return
	}
	c.JSON(200, out)
}

func (s *Server) deleteStyle(c *gin.Context) {
	if err := s.Store.DeleteStyle(c.Request.Context(), c.Param("id")); err != nil {
		fail(c, err)
		return
	}
	c.Status(204)
}

// ---------- settings ----------

func mask(v string) string {
	if v == "" {
		return ""
	}
	if len(v) <= 8 {
		return "••••"
	}
	return "••••" + v[len(v)-4:]
}

func (s *Server) getSettings(c *gin.Context) {
	st, err := s.Store.GetSettings(c.Request.Context())
	if err != nil {
		fail(c, err)
		return
	}
	eff := tasks.Resolve(s.Cfg, st)
	c.JSON(200, gin.H{
		"whisperModel": eff.WhisperModel, "whisperLanguage": eff.WhisperLanguage,
		"transcribeProvider": eff.TranscribeProvider, "fallbackProvider": eff.FallbackProvider,
		"fallbackOverMinutes": eff.FallbackOverMin, "claudeModel": eff.ClaudeModel,
		"aiProvider": eff.AIProvider, "geminiModel": eff.GeminiModel,
		"postizUrl":     eff.PostizURL,
		"suggestMinSec": eff.SuggestMinSec, "suggestMaxSec": eff.SuggestMaxSec, "suggestCount": eff.SuggestCount,
		"suggestInstructions": eff.SuggestInstruction,
		"secrets": gin.H{
			"anthropicApiKey": mask(eff.AnthropicAPIKey), "openaiApiKey": mask(eff.OpenAIAPIKey),
			"geminiApiKey": mask(eff.GeminiAPIKey),
			"groqApiKey":   mask(eff.GroqAPIKey), "postizApiKey": mask(eff.PostizAPIKey),
		},
	})
}

func (s *Server) putSettings(c *gin.Context) {
	ctx := c.Request.Context()
	cur, err := s.Store.GetSettings(ctx)
	if err != nil {
		fail(c, err)
		return
	}
	var in struct {
		store.Settings
		ClearSecrets []string `json:"clearSecrets"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		bad(c, "invalid body")
		return
	}
	next := in.Settings
	// Secrets: empty means "unchanged".
	keep := func(newV, oldV string) string {
		if strings.TrimSpace(newV) == "" {
			return oldV
		}
		return strings.TrimSpace(newV)
	}
	next.AnthropicAPIKey = keep(in.AnthropicAPIKey, cur.AnthropicAPIKey)
	next.GeminiAPIKey = keep(in.GeminiAPIKey, cur.GeminiAPIKey)
	next.OpenAIAPIKey = keep(in.OpenAIAPIKey, cur.OpenAIAPIKey)
	next.GroqAPIKey = keep(in.GroqAPIKey, cur.GroqAPIKey)
	next.PostizAPIKey = keep(in.PostizAPIKey, cur.PostizAPIKey)
	for _, k := range in.ClearSecrets {
		switch k {
		case "anthropicApiKey":
			next.AnthropicAPIKey = ""
		case "geminiApiKey":
			next.GeminiAPIKey = ""
		case "openaiApiKey":
			next.OpenAIAPIKey = ""
		case "groqApiKey":
			next.GroqAPIKey = ""
		case "postizApiKey":
			next.PostizAPIKey = ""
		}
	}
	switch next.AIProvider {
	case "", "auto", "claude", "gemini":
	default:
		bad(c, "aiProvider must be auto, claude or gemini")
		return
	}
	if !regexp.MustCompile(`^[A-Za-z0-9._\-]*$`).MatchString(next.GeminiModel + next.ClaudeModel) {
		bad(c, "invalid model name")
		return
	}
	if !regexp.MustCompile(`^[a-z0-9.\-]*$`).MatchString(next.WhisperModel) {
		bad(c, "invalid whisper model")
		return
	}
	if next.PostizURL != "" && !strings.HasPrefix(next.PostizURL, "http://") && !strings.HasPrefix(next.PostizURL, "https://") {
		bad(c, "postiz url must start with http(s)://")
		return
	}
	next.PostizURL = strings.TrimRight(next.PostizURL, "/")
	if err := s.Store.SaveSettings(ctx, &next); err != nil {
		fail(c, err)
		return
	}
	s.getSettings(c)
}

func (s *Server) uploadCookies(c *gin.Context) {
	if s.Cfg.CookiesFile == "" {
		bad(c, "YTDLP_COOKIES_FILE is not configured")
		return
	}
	fh, err := c.FormFile("file")
	if err != nil {
		bad(c, "file is required")
		return
	}
	if fh.Size > 1<<20 {
		bad(c, "cookies file too large")
		return
	}
	f, err := fh.Open()
	if err != nil {
		fail(c, err)
		return
	}
	defer f.Close()
	data, _ := io.ReadAll(io.LimitReader(f, 1<<20))
	if !strings.Contains(string(data), "\t") {
		bad(c, "expected a Netscape-format cookies.txt")
		return
	}
	if err := os.MkdirAll(filepath.Dir(s.Cfg.CookiesFile), 0o755); err != nil {
		fail(c, err)
		return
	}
	if err := os.WriteFile(s.Cfg.CookiesFile, data, 0o600); err != nil {
		fail(c, err)
		return
	}
	c.JSON(200, gin.H{"ok": true})
}

func (s *Server) deleteCookies(c *gin.Context) {
	if s.Cfg.CookiesFile != "" {
		_ = os.Remove(s.Cfg.CookiesFile)
	}
	c.JSON(200, gin.H{"ok": true})
}
