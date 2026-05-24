package view

import (
	"bytes"
	"strings"
	"testing"
)

func TestTemplatesParse(t *testing.T) {
	if _, err := New(); err != nil {
		t.Fatalf("New: %v", err)
	}
}

func TestAllPagesRender(t *testing.T) {
	r, err := New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	type tag struct{ Name string }
	type sub struct {
		ID           string
		QualityScore int
		Summary      string
		Status       string
		Tags         []tag
		IsNew        bool
		IsAdmin      bool
	}

	cases := []struct {
		name string
		data any
	}{
		{"submit", map[string]any{"TurnstileSite": ""}},
		{"dashboard", map[string]any{
			"Tags":        map[string]int64{"ai": 3},
			"MinScore":    0,
			"StarredOnly": false,
			"NewID":       "",
			"Submissions": []sub{{ID: "x", QualityScore: 80, Summary: "test", Status: "ready", Tags: []tag{{Name: "ai"}}}},
		}},
		{"video", map[string]any{
			"ID": "x", "Status": "ready", "Summary": "test",
			"Transcript": "hello world", "Tags": []tag{{Name: "ai"}},
			"QualityScore": 80,
			"DurationSec":  30, "HasVideo": true, "HasAudio": true,
		}},
		{"list_fragment", map[string]any{
			"Submissions": []sub{{ID: "x", QualityScore: 80, Summary: "test", Status: "ready", Tags: []tag{{Name: "ai"}}}},
			"NewID":       "",
		}},
		{"card_fragment", sub{ID: "x", QualityScore: 80, Summary: "test", Status: "ready", Tags: []tag{{Name: "ai"}}}},
		{"live_fragment", map[string]any{
			"ID": "x", "Status": "processing", "Summary": "",
			"Transcript": "", "Tags": []tag{}, "QualityScore": 0,
		}},
		{"status_fragment", map[string]any{"Status": "processing"}},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := r.Render(&buf, c.name, c.data); err != nil {
				t.Fatalf("render %s: %v", c.name, err)
			}
			if buf.Len() == 0 {
				t.Fatalf("render %s produced empty output", c.name)
			}
		})
	}
}

// Card fragments are the per-card polling units. While processing, the card
// must include the hx-trigger that drives the poll; once terminal, the trigger
// must be absent so HTMX stops polling for that card.
func TestCardFragment_OmitsPollerWhenReady(t *testing.T) {
	r, _ := New()
	type tag struct{ Name string }
	type sub struct {
		ID           string
		QualityScore int
		Summary      string
		Status       string
		Tags         []tag
		IsNew        bool
		IsAdmin      bool
	}

	procBuf := &bytes.Buffer{}
	if err := r.Render(procBuf, "card_fragment", sub{ID: "p", Status: "processing", Summary: "", Tags: nil}); err != nil {
		t.Fatalf("render processing: %v", err)
	}
	if !strings.Contains(procBuf.String(), `hx-trigger="every 4s"`) {
		t.Fatalf("processing card should include hx-trigger, got:\n%s", procBuf.String())
	}

	readyBuf := &bytes.Buffer{}
	if err := r.Render(readyBuf, "card_fragment", sub{ID: "r", Status: "ready", Summary: "ok", QualityScore: 80, Tags: []tag{{Name: "x"}}}); err != nil {
		t.Fatalf("render ready: %v", err)
	}
	if strings.Contains(readyBuf.String(), `hx-trigger`) {
		t.Fatalf("ready card should not include hx-trigger, got:\n%s", readyBuf.String())
	}
}
