// Package seo renders SEO metadata, public sitemaps and robots.txt.
//
// When the redirects module is registered, sitemap entries use the final
// same-origin URL after native redirect resolution. Redirect targets must be
// declared indexable entries or localized listings with indexable public items;
// unknown, protected, external and cyclic destinations are omitted. Generating a
// sitemap never requests redirect destinations or increments their hit counts.
package seo
