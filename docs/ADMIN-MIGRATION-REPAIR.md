# Migration list audit repair


## Scope

Each migration now reports its own saved queue rows grouped by actual kind and pipeline status. Preview/planned rows stay separate from done rows, even though overall pipeline progress historically groups them together. The UI does not call preview rows imported and does not infer successful final validation from a done item. Content, media, products, redirects, link rewrites, authors, terms and menus use their existing kind labels. Counts are read in the same listing query and omitted from the public JSON contract; no import writes, schema or validation semantics change.
