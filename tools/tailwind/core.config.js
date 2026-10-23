// Core site stylesheet, built into web/site/core.css and served by the binary
// at /static/site/core.css. Every theme links it through {{sections .}}, so the
// shared functional templates (store, cart, checkout, account, product, intake,
// form result) look right under any theme, not only under default.
//
//   - Only the utilities those templates and the commerce fragments use.
//   - No preflight: the reset would flatten each theme's typography.
//   - Scoped under .gp-core (important: '.gp-core'), which those templates wrap
//     their content in, so a utility name can never restyle a theme's own markup.
//   - core.css appends element defaults with zero specificity (:where), so any
//     theme rule still wins.
const base = require('./tailwind.config.js');

module.exports = {
  ...base,
  content: {
    ...base.content,
    files: [
      '../../themes/default/templates/{account,cart,checkout,checkout-result,shop,product,private-intake,form-result}.html',
      '../../internal/commerce/*.go',
      '!../../internal/**/*_test.go',
    ],
  },
  important: '.gp-core',
  corePlugins: { preflight: false },
};
