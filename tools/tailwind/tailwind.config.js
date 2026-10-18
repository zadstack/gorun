// Tailwind build for the default theme (and its children that keep its head,
// such as docs). Built by tools/tailwind/build.sh with the pinned standalone
// CLI into themes/default/assets/tailwind.css, which is committed: no Node at
// runtime. CI fails when the committed file is stale (build.sh --check).
//
// Tailwind only emits classes it finds in `content`, so every place that can
// put a utility class on a public page is scanned:
//   - default and docs theme templates, parts, components and scripts;
//   - Go code that renders public markup (builder sections, commerce
//     fragments, theme funcs, starters), listed file by file below;
//   - the bundled starter content.
// Content HTML from editors cannot add utilities: the sanitizer keeps only
// ql-*, gp-* and language-* classes (internal/content/sanitize.go), which are
// styled by the theme CSS, not Tailwind. Classes built at runtime from data go
// in safelist.txt. Anything else (custom themes, extension templates, template
// overrides with new classes) can opt into the runtime: see docs/THEMES.md.
const fs = require('fs');
const path = require('path');

const safelist = fs
  .readFileSync(path.join(__dirname, 'safelist.txt'), 'utf8')
  .split('\n')
  .map((line) => line.replace(/#.*/, '').trim())
  .filter(Boolean);

module.exports = {
  content: {
    relative: true,
    files: [
      '../../themes/default/**/*.{html,js}',
      '../../themes/docs/**/*.{html,js}',
      // Go that renders public markup. Keep this list explicit: scanning all of
      // internal/ would pull admin-only classes in. In .go files only the
      // values of class="..." attributes count (see extract below), so
      // ordinary words in Go code and comments never become CSS.
      '../../internal/theme/*.go',
      '../../internal/builder/render.go',
      '../../internal/builder/starter_*.go',
      '../../internal/commerce/*.go',
      '!../../internal/**/*_test.go',
      '../../internal/builder/static/hero-scenes.js',
      '../../internal/starter/*.xml',
    ],
    extract: {
      go: (source) =>
        [...source.matchAll(/class=\\?"([^"\\]*)/g)]
          .flatMap((m) => m[1].replace(/\{\{[^}]*\}\}/g, ' ').split(/\s+/))
          .filter(Boolean),
    },
  },
  safelist,
  theme: {
    extend: {
      colors: {
        bg: 'var(--bg)', panel: 'var(--panel)', 'panel-2': 'var(--panel-2)', ink: 'var(--ink)', muted: 'var(--muted)',
        line: 'var(--line)', 'line-strong': 'var(--line-strong)', navy: 'var(--navy)',
        green: { DEFAULT: 'var(--green)', hover: 'var(--green-hover)', soft: 'var(--green-soft)' },
        blue: { DEFAULT: 'var(--blue)', hover: 'var(--blue-hover)', soft: 'var(--blue-soft)' },
        red: { DEFAULT: 'var(--red)', soft: 'var(--red-soft)' },
        // Text on accent fills: identity derives it from the accent; white otherwise.
        'on-accent': 'var(--on-accent, #fff)',
      },
      borderRadius: { DEFAULT: 'var(--radius)' },
      fontFamily: { sans: ['var(--font)'], ui: ['var(--font-ui)'] },
    },
  },
};
