package tasks

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/hibiken/asynq"

	"github.com/malinanu/youtubeclipping/api/internal/ffmpeg"
	"github.com/malinanu/youtubeclipping/api/internal/store"
)

// CanServeAsProxy reports whether an uploaded file can be played directly in
// the browser editor, avoiding a (potentially hours-long) transcode.
func CanServeAsProxy(pr *ffmpeg.ProbeResult, ext string) bool {
	if pr == nil || pr.Height > 1080 || pr.Width > 1920*2 {
		return false
	}
	switch pr.VideoCodec {
	case "h264", "vp9", "av1":
	default:
		return false
	}
	switch pr.AudioCodec {
	case "aac", "mp3", "opus", "vorbis":
	default:
		return false
	}
	ext = strings.ToLower(ext)
	mp4 := strings.Contains(pr.FormatName, "mp4") && (ext == "mp4" || ext == "m4v" || ext == "mov")
	webm := strings.Contains(pr.FormatName, "webm") && ext == "webm"
	return mp4 || webm
}

// uploadSource returns the absolute path of an uploaded source file.
func (h *Handlers) uploadSource(v *store.Video) (string, error) {
	p, ok := h.Layout.Abs(v.SourcePath())
	if !ok {
		return "", errors.New("uploaded file is missing; upload it again")
	}
	if _, err := os.Stat(p); err != nil {
		return "", fmt.Errorf("uploaded file is missing (%v); upload it again", err)
	}
	return p, nil
}

// ingestUpload prepares an uploaded file: probe, thumbnail, editor proxy, audio.
func (h *Handlers) ingestUpload(ctx context.Context, v *store.Video) error {
	permanent := func(msg string) error {
		h.videoStatus(ctx, v.ID, "failed", "", 0, msg)
		return fmt.Errorf("%w: %s", asynq.SkipRetry, msg)
	}
	src, err := h.uploadSource(v)
	if err != nil {
		return permanent(err.Error())
	}
	um, _ := v.UploadInfo()
	dir := h.Layout.VideoDir(v.ID)

	h.videoStatus(ctx, v.ID, "downloading", "reading file", 0, "")
	pr, err := h.FF.Probe(ctx, src)
	if err != nil {
		return permanent("could not read the uploaded file — is it a valid video? " + err.Error())
	}
	if pr.VideoCodec == "" {
		return permanent("the uploaded file has no video track")
	}
	if !pr.HasAudio {
		return permanent("the uploaded file has no audio track (needed for transcription and captions)")
	}
	if pr.DurationSec <= 0 {
		return permanent("could not determine the video duration")
	}
	if pr.DurationSec > 8*3600 {
		return permanent("video is longer than 8 hours")
	}

	thumb := filepath.Join(dir, "thumb.jpg")
	thumbURL := ""
	if err := h.FF.Thumbnail(ctx, src, thumb, pr.DurationSec*0.1); err == nil {
		thumbURL = "/api/videos/" + v.ID + "/thumb"
	} else {
		h.Log.Warn("upload thumbnail", "video", v.ID, "err", err)
	}
	if err := h.Store.SetVideoMeta(ctx, v.ID, store.VideoMeta{
		Title: v.Title, Channel: "Uploaded file", DurationMs: int64(pr.DurationSec * 1000),
		Width: pr.Width, Height: pr.Height, Thumbnail: thumbURL, Meta: v.Meta,
	}); err != nil {
		return err
	}

	proxy := src
	if !CanServeAsProxy(pr, um.Ext) {
		h.videoStatus(ctx, v.ID, "downloading", "preparing preview", 0, "")
		proxy = filepath.Join(dir, "proxy.mp4")
		tmp := filepath.Join(dir, "proxy.tmp.mp4")
		if err := h.FF.TranscodeProxy(ctx, src, tmp, h.Cfg.ProxyHeight, pr.DurationSec, func(f float64) {
			h.videoProgress(ctx, v.ID, "downloading", "preparing preview", float32(f*0.9))
		}); err != nil {
			_ = os.Remove(tmp)
			return h.failVideo(ctx, v.ID, err)
		}
		if err := os.Rename(tmp, proxy); err != nil {
			return err
		}
	}

	h.videoStatus(ctx, v.ID, "downloading", "extracting audio", 0.95, "")
	wav := filepath.Join(dir, "audio.wav")
	if err := h.FF.ExtractAudio(ctx, src, wav); err != nil {
		return h.failVideo(ctx, v.ID, err)
	}
	if err := h.Store.SetVideoPaths(ctx, v.ID, h.Layout.Rel(proxy), h.Layout.Rel(wav)); err != nil {
		return err
	}
	h.videoStatus(ctx, v.ID, "transcribing", "queued", 0, "")
	return h.Enq.Transcribe(ctx, v.ID)
}
