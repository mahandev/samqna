package pipeline

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"samqna/model"
	"samqna/storage"

	"github.com/stretchr/testify/require"
)

func TestWhisperStage_Transcribes(t *testing.T) {
	bin := os.Getenv("WHISPER_BIN")
	mdl := os.Getenv("WHISPER_TEST_MODEL") // point at tiny.en for CI speed
	if bin == "" || mdl == "" {
		t.Skip("WHISPER_BIN or WHISPER_TEST_MODEL not set; skipping")
	}
	if _, err := exec.LookPath(bin); err != nil {
		t.Skipf("whisper binary not found at %s", bin)
	}
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed; skipping")
	}

	root := t.TempDir()
	st := storage.New(root, root)
	id := model.NewSubmissionID()
	createdAt := time.Now()
	paths := st.PathsFor(id, createdAt)
	require.NoError(t, st.EnsureDir(paths))

	// build audio fixture by extracting from sample.mp4
	in, err := os.ReadFile("../testdata/sample.mp4")
	require.NoError(t, err)
	src := paths.Original + ".mp4"
	require.NoError(t, os.WriteFile(src, in, 0o644))
	es := &ExtractStage{Storage: st, FfmpegBin: "ffmpeg"}
	sub := &model.Submission{ID: id, CreatedAt: createdAt, VideoPath: &src}
	require.NoError(t, es.Run(context.Background(), sub))

	ws := &WhisperStage{Bin: bin, ModelPath: mdl}
	require.NoError(t, ws.Run(context.Background(), sub))
	require.NotNil(t, sub.Transcript)
	require.NotEmpty(t, strings.TrimSpace(*sub.Transcript))
	require.Equal(t, "tag_grade", ws.Next())
}
