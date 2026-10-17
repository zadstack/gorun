# Native template editor

CodeMirror 6 progressively enhances the native template textarea. Only the
appearance template editor loads this self-hosted ESM bundle; public pages and
other admin pages do not. HTML includes CSS/JavaScript syntax support inside
style/script elements. Go template correctness is still validated by the server.
No client-side validation replaces server publication checks.

Rebuild from pinned, integrity-locked npm packages:

```sh
npm ci --prefix tools/template-editor
npm run build --prefix tools/template-editor
```

The bundled runtime has no CDN or network dependency. Keep vendor
`codemirror-editor.LICENSE` and provenance alongside it. The native textarea is
synchronously updated with each edit, retaining the existing form, CSRF,
draft/publish/delete and preview behavior. A failed module load leaves the
textarea visible. Tab leaves the editor normally; Escape focuses the next
publication control without submitting. Code is LTR within the RTL admin shell.

The bundle is about 555 KiB uncompressed / 189 KiB gzip and loads only on the
editing route. No Node.js dependency is needed at runtime.
