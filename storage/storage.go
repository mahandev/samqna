package storage

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

type Paths struct {
	Dir       string
	Original  string // extension appended at write time
	Audio     string
	Thumbnail string
}

type Storage struct {
	MediaRoot  string
	ExportRoot string
}

func New(mediaRoot, exportRoot string) *Storage {
	return &Storage{MediaRoot: mediaRoot, ExportRoot: exportRoot}
}

func (s *Storage) PathsFor(id string, createdAt time.Time) Paths {
	dir := filepath.Join(
		s.MediaRoot,
		fmt.Sprintf("%04d", createdAt.Year()),
		fmt.Sprintf("%02d", createdAt.Month()),
		fmt.Sprintf("%02d", createdAt.Day()),
		id,
	)
	return Paths{
		Dir:       dir,
		Original:  filepath.Join(dir, "original"),
		Audio:     filepath.Join(dir, "audio.opus"),
		Thumbnail: filepath.Join(dir, "thumb.jpg"),
	}
}

func (s *Storage) EnsureDir(p Paths) error {
	return os.MkdirAll(p.Dir, 0o755)
}

func (s *Storage) ExportPath(id string) string {
	return filepath.Join(s.ExportRoot, id+".mp4")
}

func (s *Storage) BatchPath(jobID string) string {
	return filepath.Join(s.ExportRoot, "batch-"+jobID+".zip")
}

func (s *Storage) EnsureRoots() error {
	for _, d := range []string{s.MediaRoot, s.ExportRoot} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	return nil
}
