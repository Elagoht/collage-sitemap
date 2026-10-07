// Package sitemap is a collage plugin that serves /sitemap.xml from the pages the
// application registered.
//
//	app, err := collage.New(&collage.Config{
//		Plugins: []collage.Plugin{sitemap.New(sitemap.Options{
//			BaseURL: "https://example.com",
//		})},
//	})
//
// Every page with a path is listed, in every locale it has one, and a page whose
// path has a {param} is listed once for each value its WithStaticParams returns —
// the same URLs a static build writes. A page in several locales carries its other
// locales as hreflang alternates. Past 50,000 URLs, the limit of one sitemap file,
// /sitemap.xml becomes an index of numbered files.
//
// The sitemap is a document like any other: cached, exported by a static build,
// and re-made when one of its tags is invalidated.
package sitemap

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Elagoht/collage/pkg/collage"
)

// Name is the plugin's name, and the key its configuration is found under.
const Name = "elagoht/sitemap"

// Tag is the dependency tag the sitemap's documents carry. Invalidate it when a
// page's static params change — a post published, a product removed — and the
// sitemap is made again.
const Tag = "sitemap"

// MaxURLs is how many URLs one sitemap file may hold, by the protocol.
const MaxURLs = 50000

// Options configures the plugin.
type Options struct {
	// BaseURL is the site's origin, as a reader's browser reaches it:
	// "https://example.com". A sitemap lists absolute URLs, and the application
	// cannot know which host it is served under. Falls back to the application's
	// Config.BaseURL when left empty, and then to the origin collage resolves for
	// the request's host (elagoht/tenant); required only when neither exists.
	BaseURL string `json:"baseURL"`
	// Path is where the sitemap is served. Default "/sitemap.xml". The numbered
	// files of an index are served under it without the extension:
	// "/sitemap/1.xml", "/sitemap/2.xml".
	Path string `json:"path"`
	// Exclude names pages to leave out: a thank-you page, a search page, anything
	// a search engine has no reason to index.
	Exclude []string `json:"exclude"`
	// LastMod returns when the page at u last changed, or the zero time when it
	// does not know; a zero time leaves <lastmod> out. Optional, and only from Go.
	LastMod func(ctx context.Context, page string, u collage.PageURL) time.Time `json:"-"`
	// MaxURLs lowers the number of URLs per file below MaxURLs. Zero is MaxURLs.
	MaxURLs int `json:"maxURLs"`
}

// Plugin serves the sitemap.
type Plugin struct {
	opts Options
	host collage.Host
}

// New returns a plugin with opts as its starting point, which the application's
// own configuration is then decoded over.
func New(opts Options) *Plugin { return &Plugin{opts: opts} }

func (p *Plugin) Name() string                   { return Name }
func (p *Plugin) Version() string                { return "0.2.1" }
func (p *Plugin) Shutdown(context.Context) error { return nil }

// ErrNoBaseURL is returned by Init when no BaseURL is configured.
var ErrNoBaseURL = errors.New("sitemap: BaseURL is required: a sitemap lists absolute URLs")

// Init reads the configuration and registers the sitemap and its numbered files.
func (p *Plugin) Init(_ context.Context, host collage.Host) error {
	p.host = host
	opts, err := collage.PluginConfig(host, p.opts)
	if err != nil {
		return err
	}
	p.opts = opts
	if p.opts.Path == "" {
		p.opts.Path = "/sitemap.xml"
	}
	if p.opts.MaxURLs <= 0 || p.opts.MaxURLs > MaxURLs {
		p.opts.MaxURLs = MaxURLs
	}
	// The plugin's own BaseURL wins. Without one, the origin is collage's for the
	// request's host — Config.BaseURL, or a resolver's on a site served on
	// several hosts — read per render.
	if p.opts.BaseURL != "" {
		base, err := url.Parse(p.opts.BaseURL)
		if err != nil || base.Scheme == "" || base.Host == "" {
			return ErrNoBaseURL
		}
		p.opts.BaseURL = strings.TrimSuffix(p.opts.BaseURL, "/")
	} else if !canResolve(host) {
		return ErrNoBaseURL
	}
	if !strings.HasSuffix(p.opts.Path, ".xml") {
		return fmt.Errorf("sitemap: path %q must end in .xml", p.opts.Path)
	}

	index := collage.NewDocument(Name, "application/xml; charset=utf-8").
		AtRoot(p.opts.Path).
		WithHandler(p.serveIndex).
		Static().
		Build()
	if err := host.RegisterDocument(index); err != nil {
		return fmt.Errorf("sitemap: %w", err)
	}
	part := collage.NewDocument(Name+":part", "application/xml; charset=utf-8").
		AtRoot(p.partPattern()).
		WithHandler(p.servePart).
		WithStaticParams(p.partParams).
		Static().
		Build()
	if err := host.RegisterDocument(part); err != nil {
		return fmt.Errorf("sitemap: %w", err)
	}
	return nil
}

// partPattern is where the numbered files of an index are served:
// "/sitemap.xml" → "/sitemap/{part}", part being "1.xml", "2.xml". A placeholder
// is a whole path segment, so the extension is part of its value.
func (p *Plugin) partPattern() string {
	return strings.TrimSuffix(p.opts.Path, ".xml") + "/{part}"
}

// canResolve reports whether collage can name an origin without the plugin's
// own BaseURL: from Config.BaseURL, or per host from a plugin implementing
// collage.OriginResolver.
func canResolve(host collage.Host) bool {
	if host.BaseURL() != "" {
		return true
	}
	origins, ok := host.(collage.Origins)
	return ok && origins.Dynamic()
}

// base is the origin this render's URLs are absolute against.
func (p *Plugin) base(rc *collage.RenderContext) (string, error) {
	if p.opts.BaseURL != "" {
		return p.opts.BaseURL, nil
	}
	if origin := collage.BaseURL(rc); origin != "" {
		return origin, nil
	}
	return "", ErrNoBaseURL
}

func (p *Plugin) partURL(base string, n int) string {
	return base + strings.TrimSuffix(p.opts.Path, ".xml") + "/" + partName(n)
}

func partName(n int) string { return strconv.Itoa(n) + ".xml" }

// entry is one <url>: a page in one locale, with its other locales.
type entry struct {
	loc        string
	lastMod    time.Time
	alternates []alternate
}

type alternate struct {
	locale, href string
}

// entries lists every URL the sitemap holds, in a stable order.
func (p *Plugin) entries(ctx context.Context, base string) ([]entry, error) {
	excluded := make(map[string]bool, len(p.opts.Exclude))
	for _, name := range p.opts.Exclude {
		excluded[name] = true
	}
	defaultLocale, _ := p.host.Locales()

	pages := p.host.Pages()
	sort.Slice(pages, func(i, j int) bool { return pages[i].Name < pages[j].Name })
	var out []entry
	for _, page := range pages {
		if excluded[page.Name] {
			continue
		}
		urls, err := p.host.PageURLs(ctx, page.Name)
		if err != nil {
			return nil, err
		}
		// The same page in several locales: URLs whose parameters match.
		groups := make(map[string][]collage.PageURL)
		var keys []string
		for _, u := range urls {
			key := paramsKey(u.Params)
			if _, seen := groups[key]; !seen {
				keys = append(keys, key)
			}
			groups[key] = append(groups[key], u)
		}
		for _, key := range keys {
			group := groups[key]
			var alts []alternate
			if len(group) > 1 {
				for _, u := range group {
					alts = append(alts, alternate{locale: u.Locale, href: base + u.Path})
					if u.Locale == defaultLocale {
						alts = append(alts, alternate{locale: "x-default", href: base + u.Path})
					}
				}
			}
			for _, u := range group {
				e := entry{loc: base + u.Path, alternates: alts}
				if p.opts.LastMod != nil {
					e.lastMod = p.opts.LastMod(ctx, page.Name, u)
				}
				out = append(out, e)
			}
		}
	}
	return out, nil
}

func paramsKey(params map[string]string) string {
	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		b.WriteString(k)
		b.WriteByte(0)
		b.WriteString(params[k])
		b.WriteByte(0)
	}
	return b.String()
}

// serveIndex answers the sitemap's own path: the URLs themselves while they fit
// in one file, an index of the numbered files once they do not.
func (p *Plugin) serveIndex(ctx context.Context, rc *collage.RenderContext) ([]byte, []string, error) {
	base, err := p.base(rc)
	if err != nil {
		return nil, nil, err
	}
	entries, err := p.entries(ctx, base)
	if err != nil {
		return nil, nil, err
	}
	if len(entries) <= p.opts.MaxURLs {
		return urlset(entries), []string{Tag}, nil
	}
	var b bytes.Buffer
	b.WriteString(xml.Header)
	b.WriteString(`<sitemapindex xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">` + "\n")
	for n := 1; n <= parts(len(entries), p.opts.MaxURLs); n++ {
		b.WriteString("  <sitemap><loc>")
		escape(&b, p.partURL(base, n))
		b.WriteString("</loc></sitemap>\n")
	}
	b.WriteString("</sitemapindex>\n")
	return b.Bytes(), []string{Tag}, nil
}

// servePart answers one numbered file of an index.
func (p *Plugin) servePart(ctx context.Context, rc *collage.RenderContext) ([]byte, []string, error) {
	number, ok := strings.CutSuffix(rc.Param("part"), ".xml")
	n, err := strconv.Atoi(number)
	if !ok || err != nil || n < 1 {
		return nil, nil, collage.ErrNotFound
	}
	base, err := p.base(rc)
	if err != nil {
		return nil, nil, err
	}
	entries, err := p.entries(ctx, base)
	if err != nil {
		return nil, nil, err
	}
	if len(entries) <= p.opts.MaxURLs || n > parts(len(entries), p.opts.MaxURLs) {
		return nil, nil, collage.ErrNotFound
	}
	from := (n - 1) * p.opts.MaxURLs
	to := min(from+p.opts.MaxURLs, len(entries))
	return urlset(entries[from:to]), []string{Tag}, nil
}

// partParams lists the numbered files a static build writes (only their count
// matters, so no origin is needed): none while the URLs fit in one.
func (p *Plugin) partParams(ctx context.Context, _ string) ([]map[string]string, error) {
	entries, err := p.entries(ctx, "")
	if err != nil {
		return nil, err
	}
	if len(entries) <= p.opts.MaxURLs {
		return nil, nil
	}
	var params []map[string]string
	for n := 1; n <= parts(len(entries), p.opts.MaxURLs); n++ {
		params = append(params, map[string]string{"part": partName(n)})
	}
	return params, nil
}

func parts(n, per int) int { return (n + per - 1) / per }

func urlset(entries []entry) []byte {
	var b bytes.Buffer
	b.WriteString(xml.Header)
	b.WriteString(`<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9" xmlns:xhtml="http://www.w3.org/1999/xhtml">` + "\n")
	for _, e := range entries {
		b.WriteString("  <url>\n    <loc>")
		escape(&b, e.loc)
		b.WriteString("</loc>\n")
		if !e.lastMod.IsZero() {
			b.WriteString("    <lastmod>")
			b.WriteString(e.lastMod.UTC().Format(time.RFC3339))
			b.WriteString("</lastmod>\n")
		}
		for _, a := range e.alternates {
			b.WriteString(`    <xhtml:link rel="alternate" hreflang="`)
			escape(&b, a.locale)
			b.WriteString(`" href="`)
			escape(&b, a.href)
			b.WriteString("\"/>\n")
		}
		b.WriteString("  </url>\n")
	}
	b.WriteString("</urlset>\n")
	return b.Bytes()
}

func escape(b *bytes.Buffer, s string) {
	_ = xml.EscapeText(b, []byte(s))
}
