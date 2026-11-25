package tasks

import (
	"testing"

	"github.com/malinanu/youtubeclipping/api/internal/ffmpeg"
)

func TestCanServeAsProxy(t *testing.T) {
	mp4 := "mov,mp4,m4a,3gp,3g2,mj2"
	cases := []struct {
		pr   ffmpeg.ProbeResult
		ext  string
		want bool
	}{
		{ffmpeg.ProbeResult{VideoCodec: "h264", AudioCodec: "aac", FormatName: mp4, Width: 1920, Height: 1080}, "mp4", true},
		{ffmpeg.ProbeResult{VideoCodec: "h264", AudioCodec: "aac", FormatName: mp4, Width: 1280, Height: 720}, "mov", true},
		{ffmpeg.ProbeResult{VideoCodec: "vp9", AudioCodec: "opus", FormatName: "matroska,webm", Height: 1080}, "webm", true},
		{ffmpeg.ProbeResult{VideoCodec: "h264", AudioCodec: "aac", FormatName: "matroska,webm", Height: 1080}, "mkv", false},
		{ffmpeg.ProbeResult{VideoCodec: "hevc", AudioCodec: "aac", FormatName: mp4, Height: 1080}, "mp4", false},
		{ffmpeg.ProbeResult{VideoCodec: "h264", AudioCodec: "ac3", FormatName: mp4, Height: 1080}, "mp4", false},
		{ffmpeg.ProbeResult{VideoCodec: "h264", AudioCodec: "aac", FormatName: mp4, Width: 3840, Height: 2160}, "mp4", false},
	}
	for i, c := range cases {
		if got := CanServeAsProxy(&c.pr, c.ext); got != c.want {
			t.Errorf("case %d (%s/%s .%s): got %v", i, c.pr.VideoCodec, c.pr.AudioCodec, c.ext, got)
		}
	}
}
