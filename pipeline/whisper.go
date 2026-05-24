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
}

func (s *WhisperStage) Name() string { return "transcribe" }
func (s *WhisperStage) Next() string { return "tag_grade" }

func (s *WhisperStage) Run(ctx context.Context, sub *model.Submission) error {
	if sub.AudioPath == "" {
		return fmt.Errorf("submission %s has no audio_path", sub.ID)
	}
	// whisper.cpp emits text to stdout when -otxt -of - is set; safer:
	// use --output-txt --output-file <prefix> and read the .txt.
	out := sub.AudioPath + ".whisper"
	cmd := exec.CommandContext(ctx, s.Bin,
		"-m", s.ModelPath,
		"-f", sub.AudioPath,
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
