package view

import (
	"bytes"
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
		Tags         []tag
	}

	cases := []struct {
		name string
		data any
	}{
		{"landing", nil},
		{"submit", map[string]any{"TurnstileSite": ""}},
		{"dashboard", map[string]any{
			"Tags":        map[string]int64{"ai": 3},
			"MinScore":    0,
			"StarredOnly": false,
			"Submissions": []sub{{ID: "x", QualityScore: 80, Summary: "test", Tags: []tag{{Name: "ai"}}}},
		}},
		{"video", map[string]any{
			"ID": "x", "Status": "ready", "Summary": "test",
			"Transcript": "hello world", "Tags": []tag{{Name: "ai"}},
			"DurationSec": 30, "HasVideo": true, "HasAudio": true,
		}},
		{"list_fragment", map[string]any{
			"Submissions": []sub{{ID: "x", QualityScore: 80, Summary: "test", Tags: []tag{{Name: "ai"}}}},
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
