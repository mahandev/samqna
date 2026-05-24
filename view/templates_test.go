package view

import "testing"

func TestTemplatesParse(t *testing.T) {
	if _, err := New(); err != nil {
		t.Fatalf("New: %v", err)
	}
}
