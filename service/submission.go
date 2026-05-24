package service

import (
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"samqna/model"
	"samqna/repository"
	"samqna/storage"
)

var (
	ErrTooLarge  = errors.New("upload too large")
	ErrRateLimit = errors.New("rate limit exceeded")
	ErrBlocked   = errors.New("blocked")
)

type Submissions struct {
	Subs     *repository.SubmissionRepo
	Jobs     *repository.JobRepo
	Tags     *repository.TagRepo
	IPs      *repository.IPRepo
	Storage  *storage.Storage
	MaxBytes int64
}

type AcceptInput struct {
	IP               string
	Email            string
	OriginalFilename string
	Reader           io.Reader
	Size             int64  // 0 if unknown
	Ext              string // ".mp4" / ".webm" — defaults to original ext
}

type AcceptResult struct {
	ID string
}

func (s *Submissions) AcceptUpload(in AcceptInput) (*AcceptResult, error) {
	if s.MaxBytes > 0 && in.Size > 0 && in.Size > s.MaxBytes {
		return nil, ErrTooLarge
	}
	if s.IPs != nil {
		if blocked, err := s.IPs.IsBlocked(in.IP); err != nil {
			return nil, err
		} else if blocked {
			return nil, ErrBlocked
		}
	}

	id := model.NewSubmissionID()
	createdAt := time.Now()
	paths := s.Storage.PathsFor(id, createdAt)
	if err := s.Storage.EnsureDir(paths); err != nil {
		return nil, err
	}

	ext := in.Ext
	if ext == "" {
		ext = ".mp4"
	}
	dst := paths.Original + ext

	f, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return nil, err
	}
	limit := s.MaxBytes
	if limit <= 0 {
		limit = 1 << 30
	}
	n, copyErr := io.Copy(f, io.LimitReader(in.Reader, limit+1))
	closeErr := f.Close()
	if copyErr != nil {
		_ = os.Remove(dst)
		return nil, copyErr
	}
	if closeErr != nil {
		_ = os.Remove(dst)
		return nil, closeErr
	}
	if n > limit {
		_ = os.Remove(dst)
		return nil, ErrTooLarge
	}

	var emailPtr *string
	if in.Email != "" {
		emailPtr = &in.Email
	}
	sub := &model.Submission{
		ID:               id,
		CreatedAt:        createdAt,
		SubmitterIP:      in.IP,
		SubmitterEmail:   emailPtr,
		OriginalFilename: in.OriginalFilename,
		SizeBytes:        n,
		VideoPath:        ptr(dst),
		AudioPath:        paths.Audio,
		ThumbnailPath:    paths.Thumbnail,
		Status:           model.StatusProcessing,
	}
	if err := s.Subs.Create(sub); err != nil {
		_ = os.Remove(dst)
		return nil, fmt.Errorf("create submission: %w", err)
	}
	if err := s.Jobs.Enqueue(sub.ID, model.StageExtract); err != nil {
		return nil, fmt.Errorf("enqueue job: %w", err)
	}
	return &AcceptResult{ID: sub.ID}, nil
}

func (s *Submissions) CheckRateLimit(ip string, max int, window time.Duration) error {
	n, err := s.Subs.CountFromIPSince(ip, time.Now().Add(-window))
	if err != nil {
		return err
	}
	if int(n) >= max {
		return ErrRateLimit
	}
	return nil
}

func ptr[T any](v T) *T { return &v }
