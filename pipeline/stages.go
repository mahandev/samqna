package pipeline

import (
	"context"

	"samqna/model"
)

type Stage interface {
	Name() string
	Run(ctx context.Context, s *model.Submission) error
	Next() string
}

type Registry struct {
	stages map[string]Stage
}

func NewRegistry() *Registry {
	return &Registry{stages: map[string]Stage{}}
}

func (r *Registry) Register(s Stage) {
	r.stages[s.Name()] = s
}

func (r *Registry) Get(name string) (Stage, bool) {
	s, ok := r.stages[name]
	return s, ok
}

type StageResult struct {
	Err       error
	NextStage string
	Terminal  bool
}

func RunStage(ctx context.Context, s Stage, sub *model.Submission) StageResult {
	if err := s.Run(ctx, sub); err != nil {
		return StageResult{Err: err}
	}
	next := s.Next()
	if next == "" {
		return StageResult{Terminal: true}
	}
	return StageResult{NextStage: next}
}
