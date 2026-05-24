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

const graderSystemPrompt = `You are an assistant that triages short user-submitted question videos for a creator's Q&A inbox. Given the transcript, return strict JSON only (no prose) matching this schema:
{"tags":[lowercase, hyphenated topic tags, max 5],
 "quality_score":0-100 integer (relevance, clarity, specificity),
 "summary":"one-line plain summary of the question",
 "is_spam":boolean (true if abusive, off-topic, promo, gibberish),
 "spam_reason":string or null}
Return only the JSON object.`

func (s *TagGradeStage) Run(ctx context.Context, sub *model.Submission) error {
	if sub.Transcript == nil || strings.TrimSpace(*sub.Transcript) == "" {
		return fmt.Errorf("submission %s has empty transcript", sub.ID)
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
	req, _ := http.NewRequestWithContext(ctx, "POST", s.Endpoint, bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+s.APIKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.Client.Do(req)
	if err != nil {
		return llmGrade{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		out, _ := io.ReadAll(resp.Body)
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
