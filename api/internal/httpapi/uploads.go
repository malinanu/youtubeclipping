package httpapi

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/gin-gonic/gin"

	"github.com/malinanu/youtubeclipping/api/internal/events"
	"github.com/malinanu/youtubeclipping/api/internal/store"
)

// MaxChunkBytes caps a single PUT. The chunk size the client uses comes from
// UPLOAD_CHUNK_MB (default 8 MiB, below a typical nginx 10m body limit).
var MaxChunkBytes int64 = 64 << 20

func (s *Server) chunkSize() int64 {
	mb := int64(s.Cfg.UploadChunkMB)
	if mb <= 0 {
		mb = 8
	}
	if mb<<20 > MaxChunkBytes {
		return MaxChunkBytes
	}
	return mb << 20
}

var uploadExts = map[string]bool{
	"mp4": true, "mov": true, "mkv": true, "webm": true, "m4v": true, "avi": true, "ts": true, "flv": true,
}

func (s *Server) partPath(id string) string {
	return filepath.Join(s.Layout.VideoDir(id), "source.part")
}

func fileSize(p string) int64 {
	st, err := os.Stat(p)
	if err != nil {
		return 0
	}
	return st.Size()
}

// freeBytes reports available space on the filesystem holding dir.
func freeBytes(dir string) (int64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(dir, &st); err != nil {
		return 0, err
	}
	return int64(st.Bavail) * int64(st.Bsize), nil
}

func (s *Server) uploadInit(c *gin.Context) {
	var in struct {
		Filename string `json:"filename"`
		Size     int64  `json:"size"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		bad(c, "invalid body")
		return
	}
	name := filepath.Base(strings.TrimSpace(in.Filename))
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(name), "."))
	if !uploadExts[ext] {
		bad(c, "unsupported file type — use mp4, mov, mkv, webm, m4v, avi, ts or flv")
		return
	}
	limit := int64(s.Cfg.MaxUploadGB) << 30
	if in.Size <= 0 {
		bad(c, "file is empty")
		return
	}
	if limit > 0 && in.Size > limit {
		bad(c, fmt.Sprintf("file is larger than the %d GB upload limit (MAX_UPLOAD_GB)", s.Cfg.MaxUploadGB))
		return
	}
	if err := s.Layout.Ensure(s.Layout.Root); err != nil {
		fail(c, err)
		return
	}
	if free, err := freeBytes(s.Layout.Root); err == nil && free < in.Size*13/10 {
		bad(c, fmt.Sprintf("not enough disk space: %.1f GB free, need about %.1f GB",
			float64(free)/(1<<30), float64(in.Size)*1.3/(1<<30)))
		return
	}
	title := strings.TrimSuffix(name, filepath.Ext(name))
	if len(title) > 200 {
		title = title[:200]
	}
	v, err := s.Store.CreateUploadVideo(c.Request.Context(), title, store.UploadMeta{Filename: name, Size: in.Size, Ext: ext})
	if err != nil {
		fail(c, err)
		return
	}
	if err := s.Layout.Ensure(s.Layout.VideoDir(v.ID)); err != nil {
		fail(c, err)
		return
	}
	f, err := os.OpenFile(s.partPath(v.ID), os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		fail(c, err)
		return
	}
	_ = f.Close()
	c.JSON(201, gin.H{"id": v.ID, "chunkSize": s.chunkSize(), "received": 0, "size": in.Size})
}

// loadUpload fetches an upload video still accepting data.
func (s *Server) loadUpload(c *gin.Context) (*store.Video, store.UploadMeta, bool) {
	v, err := s.Store.GetVideo(c.Request.Context(), c.Param("id"))
	if err != nil {
		fail(c, err)
		return nil, store.UploadMeta{}, false
	}
	um, ok := v.UploadInfo()
	if !ok {
		bad(c, "not an upload")
		return nil, um, false
	}
	return v, um, true
}

func (s *Server) uploadStatus(c *gin.Context) {
	v, um, ok := s.loadUpload(c)
	if !ok {
		return
	}
	received := fileSize(s.partPath(v.ID))
	if v.Status != "uploading" {
		received = um.Size
	}
	c.JSON(200, gin.H{"id": v.ID, "received": received, "size": um.Size, "status": v.Status, "chunkSize": s.chunkSize()})
}

func (s *Server) uploadChunk(c *gin.Context) {
	v, um, ok := s.loadUpload(c)
	if !ok {
		return
	}
	if v.Status != "uploading" {
		c.JSON(http.StatusConflict, gin.H{"error": "upload already completed", "received": um.Size})
		return
	}
	offset, err := strconv.ParseInt(c.Query("offset"), 10, 64)
	if err != nil || offset < 0 {
		bad(c, "offset is required")
		return
	}
	part := s.partPath(v.ID)
	cur := fileSize(part)
	if offset != cur {
		// Client is out of sync (retry after a lost response, or a reload): resume from here.
		c.JSON(http.StatusConflict, gin.H{"error": "offset mismatch", "received": cur})
		return
	}
	if c.Request.ContentLength > MaxChunkBytes {
		c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "chunk too large", "max": MaxChunkBytes})
		return
	}
	f, err := os.OpenFile(part, os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		fail(c, err)
		return
	}
	n, copyErr := io.Copy(f, io.LimitReader(c.Request.Body, MaxChunkBytes+1))
	closeErr := f.Close()
	if copyErr == nil && n > MaxChunkBytes {
		copyErr = errors.New("chunk too large")
	}
	if copyErr == nil && cur+n > um.Size {
		copyErr = errors.New("more data than the declared file size")
	}
	if copyErr != nil || closeErr != nil {
		// Roll back a partial chunk so the next retry starts from a clean offset.
		_ = os.Truncate(part, cur)
		msg := "upload interrupted"
		if copyErr != nil {
			msg = copyErr.Error()
		}
		c.JSON(http.StatusBadRequest, gin.H{"error": msg, "received": cur})
		return
	}
	received := cur + n
	progress := float32(float64(received) / float64(um.Size))
	ctx := c.Request.Context()
	_ = s.Store.SetVideoProgress(ctx, v.ID, progress)
	s.Bus.Publish(ctx, events.Event{Kind: "video", ID: v.ID, VideoID: v.ID, Status: "uploading", Stage: "uploading", Progress: progress}, false)
	c.JSON(200, gin.H{"received": received, "size": um.Size})
}

func (s *Server) uploadComplete(c *gin.Context) {
	v, um, ok := s.loadUpload(c)
	if !ok {
		return
	}
	if v.Status != "uploading" {
		c.JSON(200, gin.H{"status": v.Status})
		return
	}
	part := s.partPath(v.ID)
	if got := fileSize(part); got != um.Size {
		c.JSON(http.StatusConflict, gin.H{"error": fmt.Sprintf("upload incomplete: %d of %d bytes", got, um.Size), "received": got})
		return
	}
	src := filepath.Join(s.Layout.VideoDir(v.ID), "source."+um.Ext)
	if err := os.Rename(part, src); err != nil {
		fail(c, err)
		return
	}
	ctx := c.Request.Context()
	if err := s.Store.MergeVideoMeta(ctx, v.ID, map[string]any{"sourcePath": s.Layout.Rel(src)}); err != nil {
		fail(c, err)
		return
	}
	if err := s.Store.SetVideoStatus(ctx, v.ID, "queued", "", 0, ""); err != nil {
		fail(c, err)
		return
	}
	if err := s.Enq.Ingest(ctx, v.ID); err != nil {
		_ = s.Store.SetVideoStatus(ctx, v.ID, "failed", "", 0, "could not queue processing: "+err.Error())
		fail(c, err)
		return
	}
	s.Bus.Publish(ctx, events.Event{Kind: "video", ID: v.ID, VideoID: v.ID, Status: "queued"}, true)
	c.JSON(202, gin.H{"status": "queued"})
}

func (s *Server) serveThumb(c *gin.Context) {
	p := filepath.Join(s.Layout.VideoDir(c.Param("id")), "thumb.jpg")
	if _, err := os.Stat(p); err != nil {
		c.Status(http.StatusNotFound)
		return
	}
	c.Header("Cache-Control", "private, max-age=86400")
	c.File(p)
}
