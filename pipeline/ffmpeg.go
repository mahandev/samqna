package pipeline

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"

	"samqna/model"
	"samqna/storage"
)

type ExtractStage struct {
	Storage   *storage.Storage
	FfmpegBin string
}

func (s *ExtractStage) Name() string { return "extract" }
func (s *ExtractStage) Next() string { return "transcribe" }

func (s *ExtractStage) Run(ctx context.Context, sub *model.Submission) error {
	if sub.VideoPath == nil {
		return fmt.Errorf("submission %s has no video_path", sub.ID)
	}
	paths := s.Storage.PathsFor(sub.ID, sub.CreatedAt)

	// 1) audio extract → 16 kHz mono opus
	if err := runCmd(ctx, s.FfmpegBin,
		"-y", "-i", *sub.VideoPath,
		"-vn", "-ac", "1", "-ar", "16000",
		"-c:a", "libopus", "-b:a", "32k",
		paths.Audio,
	); err != nil {
		return fmt.Errorf("audio extract: %w", err)
	}

	// 2) thumbnail from middle of file
	if err := runCmd(ctx, s.FfmpegBin,
		"-y", "-i", *sub.VideoPath,
		"-vf", "thumbnail,scale=320:-1",
		"-frames:v", "1",
		paths.Thumbnail,
	); err != nil {
		return fmt.Errorf("thumbnail: %w", err)
	}

	// 3) probe duration
	dur, err := probeDuration(ctx, s.FfmpegBin, *sub.VideoPath)
	if err != nil {
		return fmt.Errorf("probe duration: %w", err)
	}
	sub.AudioPath = paths.Audio
	sub.ThumbnailPath = paths.Thumbnail
	sub.DurationSec = dur
	return nil
}

func runCmd(ctx context.Context, bin string, args ...string) error {
	cmd := exec.CommandContext(ctx, bin, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %v: %w (%s)", bin, args, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// probeDuration uses ffmpeg (not ffprobe) to keep dependency count minimal.
// Parses "Duration: hh:mm:ss.xx" from stderr.
func probeDuration(ctx context.Context, bin, path string) (int, error) {
	cmd := exec.CommandContext(ctx, bin, "-i", path, "-f", "null", "-")
	out, _ := cmd.CombinedOutput() // ffmpeg exits non-zero on -i probe; that's fine
	s := string(out)
	idx := strings.Index(s, "Duration: ")
	if idx == -1 {
		return 0, fmt.Errorf("no Duration in ffmpeg output")
	}
	rest := s[idx+len("Duration: "):]
	end := strings.Index(rest, ",")
	if end == -1 {
		return 0, fmt.Errorf("malformed Duration")
	}
	parts := strings.Split(rest[:end], ":")
	if len(parts) != 3 {
		return 0, fmt.Errorf("malformed Duration parts")
	}
	h, _ := strconv.Atoi(parts[0])
	m, _ := strconv.Atoi(parts[1])
	secFloat, _ := strconv.ParseFloat(parts[2], 64)
	return h*3600 + m*60 + int(secFloat), nil
}
