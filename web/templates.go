package web

import (
	"context"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Masterminds/sprig"
)

// TemplateManager responsible for managing, caching, finding templates
type TemplateManager interface {
	LookupTemplate(name string) (*template.Template, error)
}

func lookupTemplate(main *template.Template, name string) (*template.Template, error) {
	t := main.Lookup(name)
	if t == nil {
		return nil, fmt.Errorf("cannot find template %s", name)
	}
	return t, nil
}

type embedTM struct {
	cachedTemplates *template.Template
}

func (tm *embedTM) LookupTemplate(name string) (*template.Template, error) {
	return lookupTemplate(tm.cachedTemplates, name)
}

// NewTemplateManagerEmbed creates a TemplateManager from an embedded file system
func NewTemplateManagerEmbed(fs fs.ReadDirFS, srcDir string) (TemplateManager, error) {
	files, err := fs.ReadDir(srcDir)
	if err != nil {
		return nil, err
	}

	newFiles := fixFiles(files, srcDir)

	ts, err := baseTemplate().ParseFS(fs, newFiles...)
	if err != nil {
		return nil, fmt.Errorf("error initializing templates from embedded filesystem: %w", err)
	}
	return &embedTM{ts}, nil
}

type fsTM struct {
	srcDir string
}

func (tm *fsTM) LookupTemplate(name string) (*template.Template, error) {
	files, err := os.ReadDir(tm.srcDir)
	if err != nil {
		return nil, err
	}

	newFiles := fixFiles(files, tm.srcDir)

	main, err := baseTemplate().ParseFiles(newFiles...)
	if err != nil {
		return nil, err
	}
	return lookupTemplate(main, name)
}

// NewTemplateManagerFS creates a new TemplateManager from the file system
func NewTemplateManagerFS(srcDir string) (TemplateManager, error) {
	return &fsTM{srcDir}, nil
}

// -------------------------

// TemplateHandler implement this to be able to use middleware
type TemplateHandler interface {
	// return (template name, thing to pass to template, error)
	Serve(w http.ResponseWriter, r *http.Request) (*Template, interface{}, error)
}

// TemplateHandlerFunc the func version of the handler
type TemplateHandlerFunc func(w http.ResponseWriter, r *http.Request) (*Template, interface{}, error)

// Serve does the work
func (f TemplateHandlerFunc) Serve(w http.ResponseWriter, r *http.Request) (*Template, interface{}, error) {
	return f(w, r)
}

// Template specifies which template to render
type Template struct {
	named  string
	direct *template.Template
}

// NamedTemplate creates a Template with a name
func NamedTemplate(called string) *Template {
	return &Template{named: called}
}

// DirectTemplate creates a template to say use this specific template
func DirectTemplate(t *template.Template) *Template {
	return &Template{direct: t}
}

// TemplateMiddleware handles the rendering of the template from the data and finding of the template
type TemplateMiddleware struct {
	Templates TemplateManager
	Handler   TemplateHandler
}

func (tm *TemplateMiddleware) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	r = r.WithContext(ctx)

	t, data, err := tm.Handler.Serve(w, r)
	if HandleError(w, err) {
		return
	}

	gt := t.direct
	if gt == nil {
		gt, err = tm.Templates.LookupTemplate(t.named)
		if HandleError(w, err) {
			return
		}
	}

	HandleError(w, gt.Execute(w, data))
}

func fixFiles(files []fs.DirEntry, root string) []string {
	newFiles := []string{}
	for _, e := range files {
		x := e.Name()
		if strings.ContainsAny(x, "#~") {
			continue
		}

		newFiles = append(newFiles, filepath.Join(root, x))
	}
	return newFiles
}

func baseTemplate() *template.Template {
	return template.New("app").Funcs(sprig.FuncMap())
}
