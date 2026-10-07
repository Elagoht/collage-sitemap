# elagoht/sitemap

A collage plugin that serves `/sitemap.xml` from the pages the application
registered.

```go
app, err := collage.New(&collage.Config{
	Plugins: []collage.Plugin{sitemap.New(sitemap.Options{
		BaseURL: "https://example.com",
	})},
})
```

Requires collage v0.50.0 or later.

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
	BaseURL: "https://example.com",
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
    "baseURL": "https://example.com",
    "path": "/sitemap.xml",
    "exclude": ["thanks"],
    "maxURLs": 50000
  }
}
```

`baseURL` is the site's origin — a sitemap lists absolute URLs, and the application
cannot know which host it is served under. Leave it empty to take the application's
`Config.BaseURL` (collage v0.39.0). On a site served on several hosts, with a plugin
that resolves origins per host (elagoht/tenant), the sitemap follows
`collage.BaseURL` and names the origin of the host it is requested on. The
application does not start when there is no `baseURL`, no `Config.BaseURL` and no
resolver. Pair it with [elagoht/robots](https://github.com/Elagoht/collage-robots) to name
the sitemap in `robots.txt`.
