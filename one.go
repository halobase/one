package one

import (
	"bytes"
	"context"
	"errors"
	"html/template"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/evanw/esbuild/pkg/api"
	"github.com/russross/blackfriday/v2"
	"gopkg.in/yaml.v3"
)

const (
	defaultSourceDir = "docs"
	defaultOutputDir = "dist"

	defaultThemeDir       = "themes"
	defaultTemplateFeed   = "atom.xml"
	defaultTemplateLayout = "layout.html"
	defaultTemplatePost   = "post.html"
	defaultTemplateList   = "list.html"
	defaultTemplateIndex  = "index.html"
	defaultAssetStyle     = "index.css"
	defaultAssetScript    = "index.ts"
)

var defaultTemplates = map[string]Template{
	"index.md":  {defaultTemplateIndex, false},
	"README.md": {defaultTemplateIndex, false},
}

type One struct {
	http.Handler
	options Options
	site    Site
	funcs   template.FuncMap
	entries map[string][]*Post
}

func (o *One) Load(r io.Reader) error {
	dec := yaml.NewDecoder(r)
	return dec.Decode(&o.site.Metadata)
}

func (o *One) Dump(w io.Writer) error {
	enc := yaml.NewEncoder(w)
	return enc.Encode(&o.site.Metadata)
}

func (o *One) Bundle(ctx context.Context) error {
	opts := o.options
	entries := make([]string, 0, 2)

	style := filepath.Join(opts.ThemeDir, opts.AssetStyle)
	if _, err := os.Stat(style); err == nil {
		entries = append(entries, style)
	}

	script := filepath.Join(opts.ThemeDir, opts.AssetScript)
	if _, err := os.Stat(script); err == nil {
		entries = append(entries, script)
	}

	if len(entries) == 0 {
		return nil
	}

	result := api.Build(api.BuildOptions{
		EntryPoints:       entries,
		EntryNames:        "[dir]/[name]-[hash]",
		Outdir:            o.options.OutputDir,
		Bundle:            true,
		Write:             true,
		MinifyWhitespace:  true,
		MinifyIdentifiers: true,
		MinifySyntax:      true,
		TreeShaking:       api.TreeShakingTrue,
		Loader: map[string]api.Loader{
			".ttf": api.LoaderFile,
			".png": api.LoaderBase64,
		},
	})
	if len(result.Errors) > 0 {
		for _, err := range result.Errors {
			log.Print(err) // TODO: log error correctly
		}
		return errors.New("bundle error")
	}
	for _, file := range result.OutputFiles {
		path := filepath.Join("/", filepath.Base(file.Path))
		if strings.HasSuffix(file.Path, ".css") {
			o.site.Style = path
		}
		if strings.HasSuffix(file.Path, ".js") {
			o.site.Script = path
		}
	}
	return nil
}

func (o One) Generate(ctx context.Context) error {
	opts := o.options
	err := filepath.WalkDir(opts.SourceDir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			outdir := strings.Replace(path, opts.SourceDir, opts.OutputDir, 1)
			return os.MkdirAll(outdir, 0744)
		}
		return o.generate(ctx, path, entry)
	})
	if err != nil {
		return err
	}
	if err = o.generateList(ctx); err != nil {
		return err
	}
	return o.generateRSSFeed(ctx)
}

func toOutfile(path, src, dst string, fold bool) (string, error) {
	ext := filepath.Ext(path)
	noext := path[:len(path)-len(ext)]
	if path == src {
		// we are in single page mode
		return filepath.Join(dst, "index.html"), nil
	}
	dir := strings.Replace(noext, src, dst, 1)
	file := dir + ".html"
	if fold {
		if err := os.MkdirAll(dir, 0744); err != nil {
			return "", err
		}
		file = filepath.Join(dir, "index.html")
	}
	return file, nil
}

func (o *One) generate(ctx context.Context, path string, entry fs.DirEntry) error {
	opts := o.options
	name := entry.Name()
	use := opts.TemplatePost
	temp, ok := opts.Templates[name]
	if ok {
		use = temp.Name
	}

	t := template.New("layout").Funcs(o.funcs)
	layout := filepath.Join(opts.ThemeDir, opts.TemplateLayout)
	page := filepath.Join(opts.ThemeDir, use)
	files := []string{layout}
	if _, err := os.Stat(page); err == nil {
		files = append(files, page)
	}

	if _, err := t.ParseFiles(files...); err != nil {
		return err
	}

	outfile, err := toOutfile(path, opts.SourceDir, opts.OutputDir, !ok || temp.Fold)
	if err != nil {
		return err
	}

	out, err := os.Create(outfile)
	if err != nil {
		return err
	}
	defer out.Close()

	in, err := os.Open(path)
	if err != nil {
		return err
	}
	defer in.Close()

	info, err := entry.Info()
	if err != nil {
		return err
	}

	rawMD, err := io.ReadAll(in)
	if err != nil {
		return err
	}

	// TODO: split out front matter from rawMD

	rawHTML := blackfriday.Run(rawMD)

	post := &Post{
		Site:     o.site,
		Name:     info.Name(),
		Size:     info.Size(),
		Path:     path,
		HTML:     template.HTML(rawHTML),
		Markdown: string(rawMD),
		Metadata: Metadata{}, // TODO: extract from frontmatter
	}

	dir := filepath.Dir(path)
	o.entries[dir] = append(o.entries[dir], post)
	o.site.Posts = append(o.site.Posts, post)
	return t.ExecuteTemplate(out, "layout", post)
}

func (o One) generateList(ctx context.Context) error {
	var outdir string
	var post *Post // for xxx/index.md

	opts := o.options
	page := filepath.Join(o.options.ThemeDir, opts.TemplateList)
	if _, err := os.Stat(page); err != nil {
		return nil
	}
	layout := filepath.Join(o.options.ThemeDir, opts.TemplateLayout)
	t, err := template.New("layout").Funcs(o.funcs).ParseFiles(layout, page)
	if err != nil {
		return err
	}

	for k, v := range o.entries {
		if k == o.options.SourceDir {
			// we don't need to generate a list page for where the home page is.
			continue
		}
		outdir = strings.Replace(k, opts.SourceDir, opts.OutputDir, 1)
		out, err := os.Create(filepath.Join(outdir, "index.html"))
		if err != nil {
			return err
		}

		for _, p := range v {
			if p.Name == "index.md" {
				post = p
				break
			}
		}

		if post == nil {
			post = &Post{
				Name: filepath.Base(k),
				Path: k,
			}
		}

		list := &List{
			Site:  o.site,
			Post:  post,
			Posts: v,
		}
		if err = t.ExecuteTemplate(out, "layout", list); err != nil {
			return err
		}
	}
	return nil
}

func (o *One) generateRSSFeed(ctx context.Context) error {
	opts := o.options
	feed := filepath.Join(opts.ThemeDir, opts.TemplateFeed)
	if _, err := os.Stat(feed); err != nil {
		return nil
	}
	t, err := template.ParseFiles(feed)
	if err != nil {
		return err
	}
	w, err := os.Create(filepath.Join(o.options.OutputDir, o.options.TemplateFeed))
	if err != nil {
		return err
	}
	defer w.Close()
	return t.Execute(w, o.site)
}

func (o One) partial(path string, args interface{}) template.HTML {
	w := bytes.Buffer{}
	t := template.Must(template.ParseFiles(filepath.Join(o.options.ThemeDir, path)))
	if err := t.Execute(&w, args); err != nil {
		panic(err)
	}
	return template.HTML(w.Bytes())
}

func New(opts ...Option) *One {
	options := Options{
		Templates:      defaultTemplates,
		SourceDir:      defaultSourceDir,
		OutputDir:      defaultOutputDir,
		ThemeDir:       defaultThemeDir,
		TemplateFeed:   defaultTemplateFeed,
		TemplateLayout: defaultTemplateLayout,
		TemplatePost:   defaultTemplatePost,
		TemplateList:   defaultTemplateList,
		AssetStyle:     defaultAssetStyle,
		AssetScript:    defaultAssetScript,
	}
	for _, o := range opts {
		o(&options)
	}

	o := &One{
		options: options,
		site:    Site{},
		entries: make(map[string][]*Post),
		Handler: http.FileServer(http.Dir(options.OutputDir)),
	}

	o.funcs = template.FuncMap{
		"partial": o.partial,
	}
	return o
}

type Metadata map[string]interface{}

type Site struct {
	Style    string   `json:"style"`
	Script   string   `json:"script"`
	Posts    []*Post  `json:"posts"`
	Metadata Metadata `json:"metadata"` // extracted from config file like `one.yml`
}

type Post struct {
	Site     Site          `json:"site"`
	Name     string        `json:"name"`
	Path     string        `json:"path"`
	Markdown string        `json:"markdown"`
	HTML     template.HTML `json:"html"`
	Metadata Metadata      `json:"metadata"`
	Size     int64         `json:"size"`
}

type List struct {
	*Post
	Site  Site    `json:"site"`
	Posts []*Post `json:"posts"`
}

type Template struct {
	Name string
	Fold bool
}

type Options struct {
	SourceDir      string
	OutputDir      string
	ThemeDir       string // relative to ThemeDir
	TemplateFeed   string // relative to ThemeDir e.g. atom.xml
	TemplateLayout string // relative to ThemeDir e.g. layout.html
	TemplatePost   string // relative to ThemeDir e.g. post.html
	TemplateList   string // relative to ThemeDir e.g. list.html
	AssetStyle     string // relative to ThemeDir e.g. index.css
	AssetScript    string // relative to ThemeDir e.g. index.js
	Templates      map[string]Template
}

type Option func(*Options)

func WithSourceDir(path string) Option {
	return func(o *Options) {
		o.SourceDir = path
	}
}

func WithThemeDir(path string) Option {
	return func(o *Options) {
		o.ThemeDir = path
	}
}

func WithOutputDir(path string) Option {
	return func(o *Options) {
		o.OutputDir = path
	}
}

func WithTemplateFeed(name string) Option {
	return func(o *Options) {
		o.TemplateFeed = name
	}
}

func WithTemplateLayout(name string) Option {
	return func(o *Options) {
		o.TemplateLayout = name
	}
}

func WithTemplatePost(name string) Option {
	return func(o *Options) {
		o.TemplatePost = name
	}
}

func WithTemplateList(name string) Option {
	return func(o *Options) {
		o.TemplateList = name
	}
}

func WithTemplates(ts map[string]Template) Option {
	return func(o *Options) {
		o.Templates = ts
	}
}
