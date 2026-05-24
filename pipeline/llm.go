package pipeline

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"samqna/model"
)

type TagRepoIface interface {
	GetOrCreate(names []string) ([]model.Tag, error)
}

type TagGradeStage struct {
	Client           *http.Client
	Endpoint         string // e.g. "https://openrouter.ai/api/v1/chat/completions"
	APIKey           string
	Models           []string // fallback chain
	QualityThreshold int
	TagRepo          TagRepoIface
	AttachTags       func(sub *model.Submission, tags []model.Tag) error
}

func (s *TagGradeStage) Name() string { return "tag_grade" }
func (s *TagGradeStage) Next() string { return "" }

type llmGrade struct {
	Tags         []string `json:"tags"`
	QualityScore int      `json:"quality_score"`
	Summary      string   `json:"summary"`
	IsSpam       bool     `json:"is_spam"`
	SpamReason   *string  `json:"spam_reason"`
}

const graderSystemPrompt = `You triage short video questions submitted to Sam Sulek's open Q&A inbox.

Sam is a young bodybuilding YouTuber known for high-volume training, blunt no-fluff advice, dry humor, and off-the-cuff "in the car" vlogs. He mainly answers questions about training (hypertrophy, splits, volume, intensity, form), diet (bulking, cutting, macros, meals), supplements (creatine, protein, pre-workout), recovery (sleep, deload, soreness), body composition, and gym culture. He ALSO enjoys answering occasional off-topic fun questions when they're entertaining or unusual (relationships, hot takes, weird hypotheticals, life advice).

Given the transcript, return strict JSON only (no prose, no markdown fences) matching this schema:
{
  "tags": array of 1-5 lowercase hyphenated topic tags. Prefer specific bodybuilding tags ("hypertrophy", "deload", "cutting", "creatine", "shoulder-press", "form-check", "rest-days") over generic ones. For off-topic questions, tag the actual subject ("dating-advice", "life-philosophy", "hot-take", "random").
  "quality_score": integer 0-100. Score on a weighted blend of (a) relevance to Sam's expertise + audience, (b) specificity, (c) clarity, (d) entertainment value. Specific well-articulated bodybuilding questions land 80-95. Generic bodybuilding questions ("what should I eat?", "is creatine good?") land 50-65. Off-topic but genuinely funny / unusual / well-asked questions still score 50-70 — Sam will take a fun question over a boring training one. Vague, low-effort, repetitive, mumbled, or filler clips land below 30.
  "summary": one-line plain summary of the question (max 120 chars).
  "is_spam": true ONLY for abusive, promo, gibberish, or completely zero-effort uploads. Off-topic does NOT equal spam.
  "spam_reason": short string or null.
}
Return only the JSON object.`

func (s *TagGradeStage) Run(ctx context.Context, sub *model.Submission) error {
	if sub.Transcript == nil || strings.TrimSpace(*sub.Transcript) == "" {
		return fmt.Errorf("submission %s has empty transcript", sub.ID)
	}
	if len(s.Models) == 0 {
		return fmt.Errorf("TagGradeStage has no models configured")
	}

	var lastErr error
	var grade llmGrade
	for _, m := range s.Models {
		g, err := s.call(ctx, m, *sub.Transcript)
		if err == nil {
			grade = g
			lastErr = nil
			break
		}
		lastErr = err
	}
	if lastErr != nil {
		return fmt.Errorf("all models failed: %w", lastErr)
	}

	sub.QualityScore = &grade.QualityScore
	sub.Summary = &grade.Summary
	sub.IsSpam = grade.IsSpam
	sub.SpamReason = grade.SpamReason

	tags, err := s.TagRepo.GetOrCreate(grade.Tags)
	if err != nil {
		return fmt.Errorf("save tags: %w", err)
	}
	if err := s.AttachTags(sub, tags); err != nil {
		return fmt.Errorf("attach tags: %w", err)
	}
	if grade.IsSpam || grade.QualityScore < s.QualityThreshold {
		sub.Status = model.StatusQuarantined
	}
	return nil
}

type chatReq struct {
	Model    string `json:"model"`
	Messages []struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	} `json:"messages"`
	ResponseFormat map[string]string `json:"response_format,omitempty"`
}

type chatResp struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
}

func (s *TagGradeStage) call(ctx context.Context, model, transcript string) (llmGrade, error) {
	body, _ := json.Marshal(chatReq{
		Model: model,
		Messages: []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		}{
			{Role: "system", Content: graderSystemPrompt},
			{Role: "user", Content: transcript},
		},
		ResponseFormat: map[string]string{"type": "json_object"},
	})
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.Endpoint, bytes.NewReader(body))
	if err != nil {
		return llmGrade{}, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+s.APIKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.Client.Do(req)
	if err != nil {
		return llmGrade{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		out, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return llmGrade{}, fmt.Errorf("status %d: %s", resp.StatusCode, string(out))
	}
	var cr chatResp
	if err := json.NewDecoder(resp.Body).Decode(&cr); err != nil {
		return llmGrade{}, err
	}
	if len(cr.Choices) == 0 {
		return llmGrade{}, fmt.Errorf("no choices")
	}
	var g llmGrade
	if err := json.Unmarshal([]byte(cr.Choices[0].Message.Content), &g); err != nil {
		return llmGrade{}, fmt.Errorf("malformed grade JSON: %w", err)
	}
	return g, nil
}
