package pipeline

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"samqna/model"
)

type WhisperStage struct {
	Bin       string // path to whisper-cli (whisper.cpp binary)
	ModelPath string // path to ggml model file
	FfmpegBin string // ffmpeg binary, used to convert opus -> wav for whisper-cli
}

func (s *WhisperStage) Name() string { return "transcribe" }
func (s *WhisperStage) Next() string { return "tag_grade" }

func (s *WhisperStage) Run(ctx context.Context, sub *model.Submission) error {
	if sub.AudioPath == "" {
		return fmt.Errorf("submission %s has no audio_path", sub.ID)
	}

	// whisper.cpp's whisper-cli only reads 16 kHz mono PCM WAV. Our stored
	// audio is opus (for size). Convert to a temp wav for transcription.
	ff := s.FfmpegBin
	if ff == "" {
		ff = "ffmpeg"
	}
	wav := sub.AudioPath + ".wav"
	defer os.Remove(wav)
	conv := exec.CommandContext(ctx, ff,
		"-y", "-i", sub.AudioPath,
		"-ac", "1", "-ar", "16000",
		"-c:a", "pcm_s16le",
		"-f", "wav", wav,
	)
	if combined, err := conv.CombinedOutput(); err != nil {
		return fmt.Errorf("opus->wav: %w (%s)", err, strings.TrimSpace(string(combined)))
	}

	out := sub.AudioPath + ".whisper"
	cmd := exec.CommandContext(ctx, s.Bin,
		"-m", s.ModelPath,
		"-f", wav,
		"-l", "en",
		"-otxt",
		"-of", out,
		"-nt",
	)
	if combined, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("whisper run: %w (%s)", err, strings.TrimSpace(string(combined)))
	}
	txtPath := out + ".txt"
	data, err := os.ReadFile(txtPath)
	if err != nil {
		return fmt.Errorf("read transcript: %w", err)
	}
	tx := strings.TrimSpace(string(data))
	sub.Transcript = &tx
	return nil
}
