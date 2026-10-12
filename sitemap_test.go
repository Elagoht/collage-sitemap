package sitemap_test

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	sitemap "github.com/Elagoht/collage-sitemap"
	"github.com/Elagoht/collage/pkg/collage"
	"github.com/Elagoht/collage/pkg/collagetest"
)

// pages are the test site's pages: home and about in two locales, an English
// thank-you page, posts expanded from posts static params, and a pattern with
// none.
func pages(posts int) []collage.Registrable {
	frag := func(name string) *collage.Fragment { return collage.NewFragment(name, "p.html").Build() }
	slugs := func(context.Context, string) ([]map[string]string, error) {
		var out []map[string]string
		for i := range posts {
			out = append(out, map[string]string{"slug": "p" + string(rune('a'+i%26)) + strings.Repeat("x", i/26)})
		}
		return out, nil
	}
	return []collage.Registrable{
		collage.NewPage("home").WithContent(frag("home")).WithPath("en", "/").WithPath("tr", "/").Build(),
		collage.NewPage("about").WithContent(frag("about")).WithPath("en", "/about").WithPath("tr", "/hakkinda").Build(),
		collage.NewPage("thanks").WithContent(frag("thanks")).WithPath("en", "/thanks").Build(),
		collage.NewPage("post").WithContent(frag("post")).WithPath("en", "/posts/{slug}").WithStaticParams(slugs).Build(),
		collage.NewPage("search").WithContent(frag("search")).WithPath("en", "/search/{q}").Build(),
	}
}

// siteOptions builds the test site on baseURL, with extra options after.
func siteOptions(opts sitemap.Options, posts int, baseURL string, extra ...collagetest.SiteOption) []collagetest.SiteOption {
	return append([]collagetest.SiteOption{
		collagetest.WithoutHome(),
		collagetest.WithTemplates(map[string]string{"p.html": `<p>x</p>`}),
		collagetest.WithRegister(pages(posts)...),
		collagetest.WithConfig(func(c *collage.Config) {
			c.Locale = collage.LocaleConfig{Default: "en", Supported: []string{"en", "tr"}}
			c.BaseURL = baseURL
		}),
		collagetest.WithPlugins(sitemap.New(opts)),
	}, extra...)
}

func site(t *testing.T, opts sitemap.Options, posts int, extra ...collagetest.SiteOption) *collagetest.Site {
	t.Helper()
	return collagetest.NewSite(t, siteOptions(opts, posts, "https://example.com", extra...)...)
}

// Every page with a path, in every locale, with its alternates; a pattern's static
// params expanded, one without them left out, an excluded page left out.
func TestSitemap(t *testing.T) {
	s := site(t, sitemap.Options{
		Exclude: []string{"thanks"},
		LastMod: func(_ context.Context, page string, _ collage.PageURL) time.Time {
			if page == "about" {
				return time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
			}
			return time.Time{}
		},
	}, 2)
	body := s.Get("/sitemap.xml").WantStatus(http.StatusOK).Body
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
	s := site(t, sitemap.Options{MaxURLs: 3}, 5)
	// home ×2, about ×2, thanks, 5 posts: 10 URLs, 4 files of 3.
	index := s.Get("/sitemap.xml").Body
	if !strings.Contains(index, "<sitemapindex") || strings.Count(index, "<sitemap>") != 4 {
		t.Fatalf("index:\n%s", index)
	}
	if !strings.Contains(index, "<loc>https://example.com/sitemap/4.xml</loc>") {
		t.Errorf("index does not name the last part:\n%s", index)
	}
	total := 0
	for _, n := range []string{"1", "2", "3", "4"} {
		part := s.Get("/sitemap/" + n + ".xml").WantStatus(http.StatusOK).Body
		total += strings.Count(part, "<url>")
	}
	if total != 10 {
		t.Errorf("parts hold %d URLs, want 10", total)
	}
	if code := s.Get("/sitemap/5.xml").Status; code != http.StatusNotFound {
		t.Errorf("a part past the end = %d, want 404", code)
	}
}

// A static build writes the sitemap and, for an index, every part.
func TestSitemap_Export(t *testing.T) {
	s := site(t, sitemap.Options{MaxURLs: 3}, 5)
	out := t.TempDir()
	if _, err := collage.Build(context.Background(), s.App, collage.BuildOptions{OutDir: out}); err != nil && !errors.Is(err, collage.ErrBuildFindings) {
		t.Logf("build: %v", err)
	}
	for _, f := range []string{"sitemap.xml", "sitemap/1.xml", "sitemap/4.xml"} {
		if _, err := os.Stat(filepath.Join(out, f)); err != nil {
			t.Errorf("export lacks %s: %v", f, err)
		}
	}
}

// The plugin's own baseURL key is gone (v0.3.0): URLs follow Config.BaseURL, and
// a configuration still naming the key stops the application.
func TestSitemap_BaseURLKeyRemoved(t *testing.T) {
	_, err := collagetest.TrySite(t, siteOptions(sitemap.Options{}, 0, "https://example.com",
		collagetest.WithPluginConfig(sitemap.Name, `{"baseURL":"https://other.example"}`))...)
	if err == nil || !strings.Contains(err.Error(), `unknown key "baseURL"`) {
		t.Fatalf("start = %v, want an unknown key \"baseURL\" error", err)
	}
}

// A negative maxURLs is an error, not the default.
func TestSitemap_NegativeMaxURLs(t *testing.T) {
	_, err := collagetest.TrySite(t, siteOptions(sitemap.Options{}, 0, "https://example.com",
		collagetest.WithPluginConfig(sitemap.Name, `{"maxURLs":-1}`))...)
	if err == nil || !strings.Contains(err.Error(), "sitemap: maxURLs") {
		t.Fatalf("start = %v, want a maxURLs error", err)
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

// The sitemap is absolute against the origin collage resolves for the request's
// host: each tenant's sitemap names its own origin.
func TestSitemap_OriginFollowsHost(t *testing.T) {
	s := collagetest.NewSite(t, collagetest.WithoutHome(),
		collagetest.WithTemplates(map[string]string{"p.html": `<p>x</p>`}),
		collagetest.WithRegister(collage.NewPage("about").WithContent(collage.NewFragment("about", "p.html").Build()).WithPath("en", "/about").Build()),
		collagetest.WithConfig(func(c *collage.Config) {
			c.Cache = collage.CacheConfig{Enabled: true, Type: "memory", DefaultTTL: time.Hour}
		}),
		collagetest.WithPlugins(origins{}, sitemap.New(sitemap.Options{})),
	)
	for host, want := range map[string]string{"a.test": "https://a.example/about", "b.test": "https://b.example/about"} {
		for range 2 {
			body := s.Get("http://" + host + "/sitemap.xml").Body
			if !strings.Contains(body, "<loc>"+want+"</loc>") {
				t.Errorf("%s sitemap = %s, want %s", host, body, want)
			}
		}
	}
}

// With neither Config.BaseURL nor a resolver, Init refuses to start.
func TestSitemap_NoOriginAnywhere(t *testing.T) {
	_, err := collagetest.TrySite(t, siteOptions(sitemap.Options{}, 0, "")...)
	if !errors.Is(err, sitemap.ErrNoBaseURL) {
		t.Errorf("start = %v, want ErrNoBaseURL", err)
	}
}
