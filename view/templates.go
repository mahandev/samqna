package view

import (
	"embed"
	"html/template"
	"io"
)

//go:embed *.html components/*.html
var files embed.FS

type Renderer struct {
	pages map[string]*template.Template
}

func New() (*Renderer, error) {
	funcs := template.FuncMap{
		"truncate": func(s string, n int) string {
			if len(s) <= n {
				return s
			}
			return s[:n] + "…"
		},
	}
	parse := func(names ...string) (*template.Template, error) {
		return template.New("base").Funcs(funcs).ParseFS(files, names...)
	}
	pages := map[string]*template.Template{}
	type pageDef struct {
		name  string
		files []string
	}
	defs := []pageDef{
		{"submit", []string{"layout.html", "submit.html"}},
		{"dashboard", []string{"layout.html", "dashboard.html", "list_fragment.html", "components/card.html", "components/tag_chip.html", "components/admin_actions.html"}},
		{"video", []string{"layout.html", "video.html", "live_fragment.html", "status_fragment.html", "components/tag_chip.html"}},
		{"list_fragment", []string{"list_fragment.html", "components/card.html", "components/tag_chip.html", "components/admin_actions.html"}},
		{"card_fragment", []string{"components/card.html", "components/tag_chip.html", "components/admin_actions.html"}},
		{"live_fragment", []string{"live_fragment.html", "components/tag_chip.html"}},
		{"status_fragment", []string{"status_fragment.html"}},
		{"admin", []string{"layout.html", "admin.html"}},
	}
	for _, d := range defs {
		t, err := parse(d.files...)
		if err != nil {
			return nil, err
		}
		pages[d.name] = t
	}
	return &Renderer{pages: pages}, nil
}

// fragmentTemplates render themselves directly rather than via the "base" layout.
var fragmentTemplates = map[string]string{
	"list_fragment":   "list_fragment",
	"status_fragment": "status_fragment",
	"card_fragment":   "card",
	"live_fragment":   "live_fragment",
}

func (r *Renderer) Render(w io.Writer, name string, data any) error {
	tpl, ok := r.pages[name]
	if !ok {
		return errPageNotFound(name)
	}
	root := "base"
	if alt, ok := fragmentTemplates[name]; ok {
		root = alt
	}
	return tpl.ExecuteTemplate(w, root, data)
}

type errPageNotFound string

func (e errPageNotFound) Error() string { return "page not found: " + string(e) }
