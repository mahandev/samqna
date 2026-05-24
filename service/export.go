package service

import (
	"archive/zip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"samqna/repository"
	"samqna/storage"
)

type Export struct {
	Storage       *storage.Storage
	Subs          *repository.SubmissionRepo
	FfmpegBin     string
	MaxConcurrent int

	sem  chan struct{}
	once sync.Once
}

func (e *Export) initSem() {
	e.once.Do(func() {
		n := e.MaxConcurrent
		if n <= 0 {
			n = 2
		}
		e.sem = make(chan struct{}, n)
	})
}

func (e *Export) acquire(ctx context.Context) error {
	e.initSem()
	select {
	case e.sem <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (e *Export) release() { <-e.sem }

func (e *Export) OneClick(ctx context.Context, id string, w io.Writer) error {
	cached := e.Storage.ExportPath(id)
	if f, err := os.Open(cached); err == nil {
		defer f.Close()
		_, err := io.Copy(w, f)
		return err
	}
	sub, err := e.Subs.Get(id)
	if err != nil {
		return err
	}
	if sub.VideoPath == nil {
		return fmt.Errorf("video unavailable")
	}

	if err := e.acquire(ctx); err != nil {
		return err
	}
	defer e.release()

	args := []string{"-y", "-i", *sub.VideoPath}
	if strings.HasSuffix(strings.ToLower(*sub.VideoPath), ".mp4") {
		args = append(args, "-c", "copy", "-movflags", "+faststart", "-f", "mp4", cached)
	} else {
		args = append(args, "-c:v", "libx264", "-preset", "veryfast", "-crf", "22",
			"-c:a", "aac", "-b:a", "128k", "-movflags", "+faststart", "-f", "mp4", cached)
	}
	cmd := exec.CommandContext(ctx, e.FfmpegBin, args...)
	if combined, err := cmd.CombinedOutput(); err != nil {
		_ = os.Remove(cached)
		return fmt.Errorf("ffmpeg: %w (%s)", err, strings.TrimSpace(string(combined)))
	}
	f, err := os.Open(cached)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(w, f)
	return err
}

func (e *Export) Trim(ctx context.Context, id string, start, end float64, w io.Writer) error {
	sub, err := e.Subs.Get(id)
	if err != nil {
		return err
	}
	if sub.VideoPath == nil {
		return fmt.Errorf("video unavailable")
	}
	if end <= start {
		return fmt.Errorf("end must be after start")
	}
	if err := e.acquire(ctx); err != nil {
		return err
	}
	defer e.release()

	cmd := exec.CommandContext(ctx, e.FfmpegBin,
		"-ss", fmt.Sprintf("%.3f", start),
		"-to", fmt.Sprintf("%.3f", end),
		"-i", *sub.VideoPath,
		"-c:v", "libx264", "-preset", "veryfast", "-crf", "22",
		"-c:a", "aac", "-b:a", "128k",
		"-movflags", "frag_keyframe+empty_moov",
		"-f", "mp4", "pipe:1",
	)
	cmd.Stdout = w
	stderr, _ := cmd.StderrPipe()
	if err := cmd.Start(); err != nil {
		return err
	}
	_, _ = io.Copy(io.Discard, stderr)
	return cmd.Wait()
}

type manifestVideo struct {
	Filename     string   `json:"filename"`
	SubmissionID string   `json:"submission_id"`
	CreatedAt    string   `json:"created_at"`
	DurationSec  int      `json:"duration_sec"`
	Transcript   string   `json:"transcript,omitempty"`
	Summary      string   `json:"summary,omitempty"`
	Tags         []string `json:"tags,omitempty"`
	QualityScore *int     `json:"quality_score,omitempty"`
}

type manifest struct {
	ExportedAt string          `json:"exported_at"`
	Videos     []manifestVideo `json:"videos"`
}

func (e *Export) BatchZip(ctx context.Context, ids []string, w io.Writer) error {
	zw := zip.NewWriter(w)
	defer zw.Close()
	mf := manifest{ExportedAt: time.Now().UTC().Format("2006-01-02T15:04:05Z")}
	for i, id := range ids {
		sub, err := e.Subs.Get(id)
		if err != nil {
			return err
		}
		if sub.VideoPath == nil {
			continue
		}
		name := fmt.Sprintf("%03d-%s.mp4", i+1, sub.ID)
		fw, err := zw.Create(name)
		if err != nil {
			return err
		}
		if err := e.OneClick(ctx, sub.ID, fw); err != nil {
			return err
		}
		tagNames := []string{}
		for _, t := range sub.Tags {
			tagNames = append(tagNames, t.Name)
		}
		mf.Videos = append(mf.Videos, manifestVideo{
			Filename: name, SubmissionID: sub.ID,
			CreatedAt:    sub.CreatedAt.Format("2006-01-02T15:04:05Z"),
			DurationSec:  sub.DurationSec,
			Transcript:   derefStr(sub.Transcript),
			Summary:      derefStr(sub.Summary),
			Tags:         tagNames,
			QualityScore: sub.QualityScore,
		})
	}
	mfw, err := zw.Create("manifest.json")
	if err != nil {
		return err
	}
	enc := json.NewEncoder(mfw)
	enc.SetIndent("", "  ")
	return enc.Encode(mf)
}

func derefStr(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
