package pipeline

import (
	"context"
	"log/slog"
	"os"
	"time"

	"samqna/repository"
	"samqna/storage"
)

type Pruner struct {
	SR            *repository.SubmissionRepo
	Storage       *storage.Storage
	RetentionDays int
}

func NewPruner(sr *repository.SubmissionRepo, st *storage.Storage, days int) *Pruner {
	return &Pruner{SR: sr, Storage: st, RetentionDays: days}
}

func (p *Pruner) RunOnce(ctx context.Context) error {
	candidates, err := p.SR.PruneCandidates(p.RetentionDays)
	if err != nil {
		return err
	}
	for _, s := range candidates {
		if s.VideoPath == nil {
			continue
		}
		if err := os.Remove(*s.VideoPath); err != nil && !os.IsNotExist(err) {
			slog.Warn("prune: remove video", "id", s.ID, "err", err)
			continue
		}
		// also drop cached export if present
		_ = os.Remove(p.Storage.ExportPath(s.ID))
		if err := p.SR.MarkPruned(s.ID); err != nil {
			slog.Error("prune: mark", "id", s.ID, "err", err)
		}
	}
	return nil
}

func (p *Pruner) RunForever(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := p.RunOnce(ctx); err != nil {
				slog.Error("pruner cycle", "err", err)
			}
		}
	}
}
