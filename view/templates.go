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
		{"landing", []string{"layout.html", "landing.html"}},
		{"submit", []string{"layout.html", "submit.html"}},
		{"dashboard", []string{"layout.html", "dashboard.html", "components/card.html", "components/tag_chip.html"}},
		{"video", []string{"layout.html", "video.html", "components/tag_chip.html"}},
		{"list_fragment", []string{"list_fragment.html", "components/card.html"}},
		{"status_fragment", []string{"status_fragment.html"}},
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

func (r *Renderer) Render(w io.Writer, name string, data any) error {
	tpl, ok := r.pages[name]
	if !ok {
		return errPageNotFound(name)
	}
	// Layout templates use "base"; fragments render themselves
	root := "base"
	if name == "list_fragment" || name == "status_fragment" {
		root = name
	}
	return tpl.ExecuteTemplate(w, root, data)
}

type errPageNotFound string

func (e errPageNotFound) Error() string { return "page not found: " + string(e) }
