package server

import (
	"bytes"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"path"
	"strings"

	"github.com/predsun/dashboard/web"
)

// Renderer parses every page template at boot, paired with the shared base
// layout. Page templates expect blocks named "title" and "content" defined
// against base.html. Errors at parse time become startup errors — there is no
// "template missing" surprise at runtime.
type Renderer struct {
	pages map[string]*template.Template
}

// NewRenderer parses base.html plus each page template under templates/.
// Pages are looked up by their file name with the ".html" suffix stripped.
// Partials (filenames beginning with "_") are loaded into every page template
// so they can be referenced via {{template "_tile.html" .}} etc.
func NewRenderer() (*Renderer, error) {
	r := &Renderer{pages: map[string]*template.Template{}}

	baseRaw, err := fs.ReadFile(web.FS, "templates/base.html")
	if err != nil {
		return nil, fmt.Errorf("reading base template: %w", err)
	}

	entries, err := fs.ReadDir(web.FS, "templates")
	if err != nil {
		return nil, fmt.Errorf("reading templates dir: %w", err)
	}

	// First pass — gather partial bodies so we can attach them to every page.
	partials := map[string]string{}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".html") {
			continue
		}
		if !strings.HasPrefix(name, "_") {
			continue
		}
		raw, err := fs.ReadFile(web.FS, "templates/"+name)
		if err != nil {
			return nil, fmt.Errorf("reading partial %s: %w", name, err)
		}
		partials[name] = string(raw)
	}

	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".html") || name == "base.html" || strings.HasPrefix(name, "_") {
			continue
		}
		pageRaw, err := fs.ReadFile(web.FS, "templates/"+name)
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", name, err)
		}
		t := template.New("base.html").Funcs(funcMap())
		if _, err := t.Parse(string(baseRaw)); err != nil {
			return nil, fmt.Errorf("parsing base for %s: %w", name, err)
		}
		for pname, praw := range partials {
			if _, err := t.New(pname).Parse(praw); err != nil {
				return nil, fmt.Errorf("parsing partial %s for %s: %w", pname, name, err)
			}
		}
		if _, err := t.Parse(string(pageRaw)); err != nil {
			return nil, fmt.Errorf("parsing %s: %w", name, err)
		}
		r.pages[strings.TrimSuffix(name, ".html")] = t
	}
	return r, nil
}

// View bundles the auto-injected fields (nonce, CSRF, theme) with page-specific
// data so every template can rely on them being present.
type View struct {
	Nonce     string
	CSRFToken string
	Theme     string
	Data      any
}

// Render writes page `name` with data to w. The middleware-supplied nonce and
// CSRF token are wrapped with the page data; templates address them via
// .Nonce / .CSRFToken / .Data.X.
func (r *Renderer) Render(w http.ResponseWriter, req *http.Request, status int, name string, data any) {
	t, ok := r.pages[name]
	if !ok {
		http.Error(w, "template "+name+" not found", http.StatusInternalServerError)
		return
	}
	v := View{
		Nonce:     CSPNonce(req.Context()),
		CSRFToken: CSRFToken(req.Context()),
		Data:      data,
	}
	var buf bytes.Buffer
	if err := t.ExecuteTemplate(&buf, "base.html", v); err != nil {
		http.Error(w, "template execute: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(buf.Bytes())
}

func funcMap() template.FuncMap {
	return template.FuncMap{
		"asset":  func(p string) string { return path.Join("/static", p) },
		"upload": func(p string) string { return path.Join("/uploads", p) },
	}
}
