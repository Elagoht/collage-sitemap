# elagoht/sitemap

A collage plugin that serves `/sitemap.xml` from the pages the application
registered.

```go
app, err := collage.New(&collage.Config{
	BaseURL: "https://example.com",
	Plugins: []collage.Plugin{sitemap.New(sitemap.Options{})},
})
```

Requires collage v0.61.0 or later.

## What it lists

- Every page with a path, in every locale it has one, as `App.URL` spells it —
  locale prefix and trailing slash included.
- A page whose path has a `{param}` once for each value its `WithStaticParams`
  returns: the same URLs a static build writes. A pattern without
  `WithStaticParams` answers URLs nobody can enumerate, and is left out.
- A page in several locales with its other locales as `hreflang` alternates, and
  the default locale's URL as `x-default`.

Pages without a path — the not-found page, an error page — are never listed. Leave
out others by name:

```go
sitemap.New(sitemap.Options{
	Exclude: []string{"thanks", "search"},
})
```

## When a page changed

`<lastmod>` comes from a function, since only the application knows:

```go
LastMod: func(ctx context.Context, page string, u collage.PageURL) time.Time {
	if page == "post" {
		return posts.UpdatedAt(u.Params["slug"])
	}
	return time.Time{} // unknown: <lastmod> is left out
},
```

## Keeping it current

The sitemap is a static document: cached, exported by a static build, and re-made
when its tag is invalidated. When the pages a pattern lists change — a post
published, a product removed — invalidate `sitemap.Tag` with them:

```go
app.InvalidateTags(ctx, "post:"+slug, sitemap.Tag)
```

## Large sites

One sitemap file may hold 50,000 URLs. Past that, `/sitemap.xml` becomes a sitemap
index of numbered files — `/sitemap/1.xml`, `/sitemap/2.xml` — each holding its
share, and a static build writes them all. `MaxURLs` lowers the limit.

## Configuration

Everything but `LastMod` can come from the application's plugin configuration:

```json
{
  "elagoht/sitemap": {
    "path": "/sitemap.xml",
    "exclude": ["thanks"],
    "maxURLs": 50000
  }
}
```

A sitemap lists absolute URLs, against the origin collage resolves for the
request's host: the application's `Config.BaseURL` (`BASE_URL`), or, on a site
served on several hosts with a plugin that resolves origins per host
(elagoht/tenant), the origin of the host it is requested on. The application does
not start when there is no `Config.BaseURL` and no resolver. `maxURLs` 0 is
50,000; a negative value stops the application. Pair it with [elagoht/robots](https://github.com/Elagoht/collage-robots) to name
the sitemap in `robots.txt`.

## Changed in v0.3.0

- The `baseURL` key and `Options.BaseURL` are removed. Set the application's
  `Config.BaseURL` (`BASE_URL`) instead, or register an origin resolver; a
  configuration that still has `baseURL` stops the application with
  `unknown key "baseURL"`. A malformed origin is now refused by collage itself.
- `ErrNoBaseURL` means only "no origin anywhere", and says so.
- A negative `maxURLs` is an error; it used to mean 50,000.
