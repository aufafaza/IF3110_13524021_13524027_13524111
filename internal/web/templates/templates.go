package templates

import (
	"bytes"
	"embed"
	"fmt"
	"html/template"
	"log"
	"net/http"

	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/access"
	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/web/csrf"
)

//go:embed layout.html home.html admin/*.html db/*.html partials/*.html
var files embed.FS

var pageNames = []string{
	"home", "admin/users", "admin/groups", "admin/group",
	"admin/databases", "admin/database",
	"db/select", "db/tables", "db/table", "db/error",
}

type View struct {
	Title     string
	User      access.User
	CSRFToken string
	Data      any
}

type Set struct {
	pages map[string]*template.Template
}

func LevelLabel(l access.Level) string {
	switch l {
	case access.LevelView:
		return "View Only"
	case access.LevelEdit:
		return "View + Edit"
	}
	return "No access"
}

func New() (*Set, error) {
	funcs := template.FuncMap{
		"levelLabel": LevelLabel,
		"list":       func(v ...string) []string { return v },
	}

	pages := make(map[string]*template.Template, len(pageNames))
	for _, name := range pageNames {
		t, err := template.New("layout.html").Funcs(funcs).ParseFS(files, "layout.html", "partials/sidebar.html", name+".html")
		if err != nil {
			return nil, fmt.Errorf("templates: parse %s: %w", name, err)
		}
		pages[name] = t
	}
	return &Set{pages: pages}, nil
}

func (s *Set) Page(w http.ResponseWriter, r *http.Request, status int, page, title string, data any) {
	t, ok := s.pages[page]
	if !ok {
		log.Printf("templates: unknown page %q", page)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	user, _ := access.UserFromContext(r.Context())
	token, _ := csrf.TokenFromContext(r.Context())
	view := View{Title: title, User: user, CSRFToken: token, Data: data}

	s.write(w, status, t, "layout.html", view)
}

func (s *Set) Fragment(w http.ResponseWriter, status int, page, name string, data any) {
	t, ok := s.pages[page]
	if !ok {
		log.Printf("templates: unknown page %q", page)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	s.write(w, status, t, name, data)
}

func (s *Set) write(w http.ResponseWriter, status int, t *template.Template, name string, data any) {
	var buf bytes.Buffer
	if err := t.ExecuteTemplate(&buf, name, data); err != nil {
		log.Printf("templates: render %s: %v", name, err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(buf.Bytes())
}
