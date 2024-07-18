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
	"github.com/edaniels/golog"

	"go.viam.com/utils"
	"go.viam.com/utils/web/protojson"
)

// TemplateManager responsible for managing, caching, finding templates.
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
	protojson.MarshalingOptions

	cachedTemplates *template.Template
}

func (tm *embedTM) LookupTemplate(name string) (*template.Template, error) {
	return lookupTemplate(tm.cachedTemplates, name)
}

// NewTemplateManagerEmbed creates a TemplateManager from an embedded file system.
func NewTemplateManagerEmbed(fs fs.ReadDirFS, srcDir string) (TemplateManager, error) {
	return NewTemplateManagerEmbedWithOptions(fs, srcDir, protojson.DefaultMarshalingOptions())
}

// NewTemplateManagerEmbedWithOptions creates a TemplateManager from an embedded file system. Allows optional protojson.MarshalingOptions.
func NewTemplateManagerEmbedWithOptions(fs fs.ReadDirFS, srcDir string, opts protojson.MarshalingOptions) (TemplateManager, error) {
	files, err := fs.ReadDir(srcDir)
	if err != nil {
		return nil, err
	}

	newFiles := fixFiles(files, srcDir)

	ts, err := baseTemplate(opts).ParseFS(fs, newFiles...)
	if err != nil {
		return nil, fmt.Errorf("error initializing templates from embedded filesystem: %w", err)
	}
	return &embedTM{opts, ts}, nil
}

type fsTM struct {
	protojson.MarshalingOptions

	srcDir string
}

func (tm *fsTM) LookupTemplate(name string) (*template.Template, error) {
	files, err := os.ReadDir(tm.srcDir)
	if err != nil {
		return nil, err
	}

	newFiles := fixFiles(files, tm.srcDir)

	main, err := baseTemplate(tm.MarshalingOptions).ParseFiles(newFiles...)
	if err != nil {
		return nil, err
	}
	return lookupTemplate(main, name)
}

// NewTemplateManagerFS creates a new TemplateManager from the file system.
func NewTemplateManagerFS(srcDir string) (TemplateManager, error) {
	return NewTemplateManagerFSWithOptions(srcDir, protojson.DefaultMarshalingOptions())
}

// NewTemplateManagerFSWithOptions creates a new TemplateManager from the file system. Allows optional protojson.MarshalingOptions.
func NewTemplateManagerFSWithOptions(srcDir string, opts protojson.MarshalingOptions) (TemplateManager, error) {
	return &fsTM{opts, srcDir}, nil
}

// -------------------------

// TemplateHandler implement this to be able to use middleware.
type TemplateHandler interface {
	// return (template name, thing to pass to template, error)
	Serve(w http.ResponseWriter, r *http.Request) (*Template, interface{}, error)
}

// TemplateHandlerFunc the func version of the handler.
type TemplateHandlerFunc func(w http.ResponseWriter, r *http.Request) (*Template, interface{}, error)

// Serve does the work.
func (f TemplateHandlerFunc) Serve(w http.ResponseWriter, r *http.Request) (*Template, interface{}, error) {
	return f(w, r)
}

// Template specifies which template to render.
type Template struct {
	named  string
	direct *template.Template
}

// NamedTemplate creates a Template with a name.
func NamedTemplate(called string) *Template {
	return &Template{named: called}
}

// DirectTemplate creates a template to say use this specific template.
func DirectTemplate(t *template.Template) *Template {
	return &Template{direct: t}
}

// TemplateMiddleware handles the rendering of the template from the data and finding of the template.
type TemplateMiddleware struct {
	Templates TemplateManager
	Handler   TemplateHandler
	Logger    utils.ZapCompatibleLogger

	// Recover from panics with a proper error logs.
	PanicCapture
}

// NewTemplateMiddleware returns a configured TemplateMiddleWare with a panic capture configured.
func NewTemplateMiddleware(template TemplateManager, h TemplateHandler, logger utils.ZapCompatibleLogger) *TemplateMiddleware {
	return &TemplateMiddleware{
		Templates: template,
		Handler:   h,
		Logger:    logger,
		PanicCapture: PanicCapture{
			Logger: logger,
		},
	}
}

type responseWriterCapturer struct {
	http.ResponseWriter
	statusCode int
}

func (w *responseWriterCapturer) WriteHeader(code int) {
	w.statusCode = code
	w.ResponseWriter.WriteHeader(code)
}

func (tm *TemplateMiddleware) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Recover from panics in underlying handler.
	defer tm.Recover(w, r)

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	r = r.WithContext(ctx)

	capW := responseWriterCapturer{ResponseWriter: w}
	t, data, err := tm.Handler.Serve(&capW, r)
	if HandleError(w, err, tm.Logger) {
		return
	}
	if capW.statusCode != 0 {
		// user decided to do something else
		return
	}

	gt := t.direct
	if gt == nil {
		gt, err = tm.Templates.LookupTemplate(t.named)
		if HandleError(w, err, tm.Logger) {
			return
		}
	}

	HandleError(w, gt.Execute(w, data), tm.Logger)
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

func baseTemplate(opts protojson.MarshalingOptions) *template.Template {
	funcs := sprig.FuncMap()

	// Support optional protoJson
	funcs["protoJson"] = createToProtoJSON(opts)

	return template.New("app").Funcs(funcs)
}

// createToProtoJson returns a function to encode an item into a json inteface using the protojson marshaler.
func createToProtoJSON(opts protojson.MarshalingOptions) func(v interface{}) interface{} {
	marshaler := protojson.Marshaler{Opts: opts}

	return func(v interface{}) interface{} {
		out, err := marshaler.MarshalToInterface(v)
		if err != nil {
			golog.Global().Errorf("protoJson failed to marshal: %s", err)
		}
		return out
	}
}
