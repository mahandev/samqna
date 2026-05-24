package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"samqna/config"
	"samqna/migrations"
	"samqna/model"
	"samqna/notify"
	"samqna/pipeline"
	"samqna/repository"
	"samqna/route"
	"samqna/service"
	"samqna/storage"
	"samqna/view"

	"github.com/gin-gonic/gin"
)

type App struct {
	Cfg    *config.Config
	Router *gin.Engine
	Pool   *pipeline.Pool
	Pruner *pipeline.Pruner
	Notify *notify.Notifier
	srv    *http.Server
}

func CreateNewApp() (*App, error) {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})))

	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}
	db, err := config.ConnectDB(cfg.DatabasePath)
	if err != nil {
		return nil, err
	}
	if err := migrations.Migrate(db); err != nil {
		return nil, err
	}
	st := storage.New(cfg.MediaPath, cfg.ExportPath)
	if err := st.EnsureRoots(); err != nil {
		return nil, err
	}
	vw, err := view.New()
	if err != nil {
		return nil, err
	}

	subRepo := repository.NewSubmissionRepo(db)
	jobRepo := repository.NewJobRepo(db)
	tagRepo := repository.NewTagRepo(db)
	ipRepo := repository.NewIPRepo(db)

	subSvc := &service.Submissions{
		Subs: subRepo, Jobs: jobRepo, Tags: tagRepo, IPs: ipRepo,
		Storage: st, MaxBytes: cfg.MaxUploadBytes,
	}
	exportSvc := &service.Export{Storage: st, Subs: subRepo, FfmpegBin: cfg.FfmpegBin, MaxConcurrent: 2}

	// Pipeline
	reg := pipeline.NewRegistry()
	reg.Register(&pipeline.ExtractStage{Storage: st, FfmpegBin: cfg.FfmpegBin})
	reg.Register(&pipeline.WhisperStage{Bin: cfg.WhisperBin, ModelPath: cfg.WhisperModel})
	reg.Register(&pipeline.TagGradeStage{
		Client:           &http.Client{Timeout: 30 * time.Second},
		Endpoint:         "https://openrouter.ai/api/v1/chat/completions",
		APIKey:           cfg.OpenRouterKey,
		Models: []string{
			"google/gemini-2.5-flash",
			"google/gemini-2.0-flash-001",
			"deepseek/deepseek-chat",
			"qwen/qwen-2.5-7b-instruct",
		},
		QualityThreshold: cfg.QualityThreshold,
		TagRepo:          tagRepo,
		AttachTags: func(sub *model.Submission, tags []model.Tag) error {
			return db.Model(sub).Association("Tags").Replace(tags)
		},
	})
	pool := pipeline.NewPool(db, subRepo, jobRepo, reg, cfg.WorkerCount, 1*time.Second, 5)
	pruner := pipeline.NewPruner(subRepo, st, cfg.RetentionDays)
	n := notify.New()

	router := gin.New()
	router.Use(gin.Recovery(), slogMiddleware())
	deps := &route.Deps{
		Cfg: cfg, DB: db,
		Subs: subRepo, Jobs: jobRepo, Tags: tagRepo, IPs: ipRepo,
		Storage: st, View: vw,
		Submissions: subSvc, ExportSvc: exportSvc,
	}
	route.RegisterPublic(router, deps)
	route.RegisterAdmin(router, deps)

	app := &App{Cfg: cfg, Router: router, Pool: pool, Pruner: pruner, Notify: n}
	return app, nil
}

func (a *App) Run() error {
	a.Pool.Start()
	go a.Pruner.RunForever(context.Background(), 6*time.Hour)

	a.srv = &http.Server{Addr: ":" + a.Cfg.Port, Handler: a.Router}

	errCh := make(chan error, 1)
	go func() {
		slog.Info("listening", "port", a.Cfg.Port)
		if err := a.srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	select {
	case <-sig:
		slog.Info("shutdown signal received")
	case err := <-errCh:
		return fmt.Errorf("listen: %w", err)
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_ = a.srv.Shutdown(shutdownCtx)
	a.Pool.Stop(30 * time.Second)
	return nil
}

func slogMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		slog.Info("http",
			"method", c.Request.Method,
			"path", c.Request.URL.Path,
			"status", c.Writer.Status(),
			"dur_ms", time.Since(start).Milliseconds(),
		)
	}
}
