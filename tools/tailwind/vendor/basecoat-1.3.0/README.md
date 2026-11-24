# Basecoat 1.3.0 sources for the core site stylesheet

Build inputs only (never served). From the npm package `basecoat-css@1.3.0`
(`https://registry.npmjs.org/basecoat-css/-/basecoat-css-1.3.0.tgz`,
sha256 `89a7a5c5340e47eec34571f34be2abdaa2ccf6a75139eca1809bcf999e0bf9a6`),
MIT licence in `LICENSE.md`:

- `components/*.css`: the structural rules of the components the shared
  functional pages use (`dist/components/<name>.css`, unchanged).
- `styles/vega.css`: the default ("vega") visual rules for every component
  (`dist/styles/vega.css`, unchanged); `tools/tailwind/build.sh` extracts the
  sections named in `tools/tailwind/core-basecoat.css`.

The admin build uses the single-file bundle vendored under
`web/admin/static/vendor/` instead; both come from the same package version.
Upgrade both together: replace these files from the new package's `dist/`
and rebuild with `make css`.
