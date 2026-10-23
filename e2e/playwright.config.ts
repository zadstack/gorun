import { defineConfig, devices } from '@playwright/test';
import { execFileSync } from 'node:child_process';

// One GoRun server per run, booted by global-setup.ts on a free port. The
// port is chosen here (the runner loads this file first) and exported through
// process.env so the workers, which re-load this file, reuse it.
if (!process.env.E2E_PORT) {
  process.env.E2E_PORT = execFileSync(process.execPath, [
    '-e',
    "const s=require('net').createServer();s.listen(0,'127.0.0.1',()=>{process.stdout.write(String(s.address().port));s.close()})",
  ]).toString();
}
const baseURL = `http://127.0.0.1:${process.env.E2E_PORT}`;

export default defineConfig({
  testDir: './tests',
  outputDir: './test-results',
  globalSetup: './global-setup.ts',
  // Specs share one SQLite database (setup runs once, the theme spec changes
  // every public page): run serially, in file order.
  fullyParallel: false,
  workers: 1,
  retries: 0,
  timeout: 90_000,
  expect: { timeout: 10_000 },
  forbidOnly: !!process.env.CI,
  reporter: [['list'], ['html', { open: 'never', outputFolder: 'playwright-report' }]],
  use: {
    baseURL,
    locale: 'ar',
    trace: 'retain-on-failure',
    screenshot: 'only-on-failure',
    ...devices['Desktop Chrome'],
  },
  projects: [
    { name: 'setup', testMatch: /global\.setup\.ts/ },
    {
      name: 'chromium',
      dependencies: ['setup'],
      testIgnore: [/global\.setup\.ts/, /public-theme\.spec\.ts/, /visual\.spec\.ts/, /catalog-themes\.spec\.ts/, /theme-acceptance\.spec\.ts/, /theme-variants\.spec\.ts/, /sections-pilot\.spec\.ts/, /identity-draft-acceptance\.spec\.ts/],
      use: { storageState: '.auth/admin.json' },
    },
    {
      // Design QA: activates each catalog theme in turn (page kinds × widths),
      // exercises the site identity settings, then restores the default theme.
      // Also runs brief §6 acceptance (home + 3 inners @ 1440/390) and the
      // section-component pilot pages (default + business, ar/en, 1280/375).
      name: 'catalog',
      dependencies: ['chromium'],
      testMatch: /catalog-themes\.spec\.ts|theme-acceptance\.spec\.ts|theme-variants\.spec\.ts|sections-pilot\.spec\.ts|identity-draft-acceptance\.spec\.ts/,
      use: { storageState: '.auth/admin.json' },
    },
    {
      // Activates the Business theme (changes every public page).
      name: 'theme',
      dependencies: ['catalog'],
      testMatch: /public-theme\.spec\.ts/,
      use: { storageState: '.auth/admin.json' },
    },
    {
      // Last: visual-sanity screenshots of the finished site.
      name: 'visual',
      dependencies: ['theme'],
      testMatch: /visual\.spec\.ts/,
      use: { storageState: '.auth/admin.json' },
    },
  ],
});
