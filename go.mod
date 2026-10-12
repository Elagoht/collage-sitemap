// A collage plugin that serves sitemap.xml from the pages the application
// registered: every locale, every path WithStaticParams lists, hreflang
// alternates, and a sitemap index when there are too many URLs for one file.
//
// It requires collage the way any consumer does, and reaches nothing the framework
// does not offer every plugin.
module github.com/Elagoht/collage-sitemap

go 1.26

require github.com/Elagoht/collage v0.61.0
