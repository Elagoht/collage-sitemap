package sitemap_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	sitemap "github.com/Elagoht/collage-sitemap"
	"github.com/Elagoht/collage/pkg/collage"
)

func site(t *testing.T, opts sitemap.Options, posts int) *collage.App {
	t.Helper()
	app, err := collage.New(&collage.Config{
		Server:   collage.ServerConfig{Host: "localhost", Port: 3000},
		Template: collage.TemplateConfig{FS: fstest.MapFS{"t/p.html": {Data: []byte(`<p>x</p>`)}}, Root: "t"},
		Locale:   collage.LocaleConfig{Default: "en", Supported: []string{"en", "tr"}},
		Plugins:  []collage.Plugin{sitemap.New(opts)},
	})
	if err != nil {
		t.Fatal(err)
	}
	frag := func(name string) *collage.Fragment { return collage.NewFragment(name, "p.html").Build() }
	slugs := func(context.Context, string) ([]map[string]string, error) {
		var out []map[string]string
		for i := range posts {
			out = append(out, map[string]string{"slug": "p" + string(rune('a'+i%26)) + strings.Repeat("x", i/26)})
		}
		return out, nil
	}
	for _, page := range []*collage.Page{
		collage.NewPage("home").WithContent(frag("home")).WithPath("en", "/").WithPath("tr", "/").Build(),
		collage.NewPage("about").WithContent(frag("about")).WithPath("en", "/about").WithPath("tr", "/hakkinda").Build(),
		collage.NewPage("thanks").WithContent(frag("thanks")).WithPath("en", "/thanks").Build(),
		collage.NewPage("post").WithContent(frag("post")).WithPath("en", "/posts/{slug}").WithStaticParams(slugs).Build(),
		collage.NewPage("search").WithContent(frag("search")).WithPath("en", "/search/{q}").Build(),
	} {
		if err := app.RegisterPage(page); err != nil {
			t.Fatal(err)
		}
	}
	return app
}

func get(t *testing.T, app *collage.App, path string) (int, string) {
	t.Helper()
	rec := httptest.NewRecorder()
	app.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec.Code, rec.Body.String()
}

// Every page with a path, in every locale, with its alternates; a pattern's static
// params expanded, one without them left out, an excluded page left out.
func TestSitemap(t *testing.T) {
	app := site(t, sitemap.Options{
		BaseURL: "https://example.com/",
		Exclude: []string{"thanks"},
		LastMod: func(_ context.Context, page string, _ collage.PageURL) time.Time {
			if page == "about" {
				return time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
			}
			return time.Time{}
		},
	}, 2)
	code, body := get(t, app, "/sitemap.xml")
	if code != http.StatusOK {
		t.Fatalf("GET /sitemap.xml = %d %s", code, body)
	}
	for _, want := range []string{
		`<?xml version="1.0" encoding="UTF-8"?>`,
		`<loc>https://example.com/</loc>`,
		`<loc>https://example.com/tr</loc>`,
		`<loc>https://example.com/about</loc>`,
		`<loc>https://example.com/tr/hakkinda</loc>`,
		`<xhtml:link rel="alternate" hreflang="tr" href="https://example.com/tr/hakkinda"/>`,
		`<xhtml:link rel="alternate" hreflang="x-default" href="https://example.com/about"/>`,
		`<lastmod>2026-09-01T12:00:00Z</lastmod>`,
		`<loc>https://example.com/posts/pa</loc>`,
		`<loc>https://example.com/posts/pb</loc>`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("sitemap lacks %s\n%s", want, body)
		}
	}
	for _, unwanted := range []string{"/thanks", "/search", "{"} {
		if strings.Contains(body, unwanted) {
			t.Errorf("sitemap contains %q\n%s", unwanted, body)
		}
	}
}

// Past the limit of one file, the sitemap becomes an index of numbered files, and
// each holds its share.
func TestSitemap_Index(t *testing.T) {
	app := site(t, sitemap.Options{BaseURL: "https://example.com", MaxURLs: 3}, 5)
	// home ×2, about ×2, thanks, 5 posts: 10 URLs, 4 files of 3.
	_, index := get(t, app, "/sitemap.xml")
	if !strings.Contains(index, "<sitemapindex") || strings.Count(index, "<sitemap>") != 4 {
		t.Fatalf("index:\n%s", index)
	}
	if !strings.Contains(index, "<loc>https://example.com/sitemap/4.xml</loc>") {
		t.Errorf("index does not name the last part:\n%s", index)
	}
	total := 0
	for _, n := range []string{"1", "2", "3", "4"} {
		code, part := get(t, app, "/sitemap/"+n+".xml")
		if code != http.StatusOK {
			t.Fatalf("part %s = %d", n, code)
		}
		total += strings.Count(part, "<url>")
	}
	if total != 10 {
		t.Errorf("parts hold %d URLs, want 10", total)
	}
	if code, _ := get(t, app, "/sitemap/5.xml"); code != http.StatusNotFound {
		t.Errorf("a part past the end = %d, want 404", code)
	}
}

// A static build writes the sitemap and, for an index, every part.
func TestSitemap_Export(t *testing.T) {
	app := site(t, sitemap.Options{BaseURL: "https://example.com", MaxURLs: 3}, 5)
	out := t.TempDir()
	builder, err := collage.NewBuilder(app, collage.BuildOptions{OutDir: out})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := builder.Build(context.Background()); err != nil && !errors.Is(err, collage.ErrBuildFindings) {
		t.Logf("build: %v", err)
	}
	for _, f := range []string{"sitemap.xml", "sitemap/1.xml", "sitemap/4.xml"} {
		if _, err := os.Stat(filepath.Join(out, f)); err != nil {
			t.Errorf("export lacks %s: %v", f, err)
		}
	}
}

func TestSitemap_NeedsABaseURL(t *testing.T) {
	app := site(t, sitemap.Options{}, 0)
	rec := httptest.NewRecorder()
	app.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("an application whose sitemap has no base URL served %d, want it not to start", rec.Code)
	}
}

// TestSitemap_FallsBackToConfigBaseURL: with no BaseURL of its own, the plugin
// takes the application's Config.BaseURL, so a site configures its origin once.
func TestSitemap_FallsBackToConfigBaseURL(t *testing.T) {
	app, err := collage.New(&collage.Config{
		Server:   collage.ServerConfig{Host: "localhost", Port: 3000},
		Template: collage.TemplateConfig{FS: fstest.MapFS{"t/p.html": {Data: []byte(`<p>x</p>`)}}, Root: "t"},
		Locale:   collage.LocaleConfig{Default: "en", Supported: []string{"en"}},
		BaseURL:  "https://fromconfig.example",
		Plugins:  []collage.Plugin{sitemap.New(sitemap.Options{})}, // no BaseURL of its own
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	page := collage.NewPage("home").WithContent(collage.NewFragment("home", "p.html").Build()).WithPath("en", "/").Build()
	if err := app.RegisterPage(page); err != nil {
		t.Fatal(err)
	}
	code, body := get(t, app, "/sitemap.xml")
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200 — the sitemap should take the app's Config.BaseURL", code)
	}
	if !strings.Contains(body, "https://fromconfig.example/") {
		t.Errorf("sitemap = %q, want it built on the app's Config.BaseURL", body)
	}
}

// origins resolves two hosts, as elagoht/tenant would.
type origins struct{}

func (origins) Name() string                             { return "test/origins" }
func (origins) Version() string                          { return "0" }
func (origins) Init(context.Context, collage.Host) error { return nil }
func (origins) Shutdown(context.Context) error           { return nil }
func (origins) Origin(_ context.Context, host string) (string, bool) {
	switch host {
	case "a.test":
		return "https://a.example", true
	case "b.test":
		return "https://b.example", true
	}
	return "", false
}

// Without a BaseURL of its own, the sitemap is absolute against the origin collage
// resolves for the request's host: each tenant's sitemap names its own origin.
func TestSitemap_OriginFollowsHost(t *testing.T) {
	app, err := collage.New(&collage.Config{
		Server:   collage.ServerConfig{Host: "localhost", Port: 3000},
		Template: collage.TemplateConfig{FS: fstest.MapFS{"t/p.html": {Data: []byte(`<p>x</p>`)}}, Root: "t"},
		Cache:    collage.CacheConfig{Enabled: true, Type: "memory", DefaultTTL: time.Hour},
		Plugins:  []collage.Plugin{origins{}, sitemap.New(sitemap.Options{})},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := app.RegisterPage(collage.NewPage("about").WithContent(collage.NewFragment("about", "p.html").Build()).WithPath("en", "/about").Build()); err != nil {
		t.Fatal(err)
	}
	for host, want := range map[string]string{"a.test": "https://a.example/about", "b.test": "https://b.example/about"} {
		for range 2 {
			rec := httptest.NewRecorder()
			app.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "http://"+host+"/sitemap.xml", nil))
			if !strings.Contains(rec.Body.String(), "<loc>"+want+"</loc>") {
				t.Errorf("%s sitemap = %s, want %s", host, rec.Body.String(), want)
			}
		}
	}
}

// With neither a BaseURL nor a resolver, Init still refuses to start. Plugin Init
// runs lazily, so the error surfaces from Start.
func TestSitemap_NoOriginAnywhere(t *testing.T) {
	app, err := collage.New(&collage.Config{
		Server:   collage.ServerConfig{Host: "localhost", Port: 3000},
		Template: collage.TemplateConfig{FS: fstest.MapFS{"t/p.html": {Data: []byte(`x`)}}, Root: "t"},
		Plugins:  []collage.Plugin{sitemap.New(sitemap.Options{})},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := app.Start(); !errors.Is(err, sitemap.ErrNoBaseURL) {
		t.Errorf("Start = %v, want ErrNoBaseURL", err)
	}
}
