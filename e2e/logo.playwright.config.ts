import { defineConfig, devices } from "@playwright/test";
import { execFileSync } from "node:child_process";

// Dedicated test executable: the production server has no fake-provider switch.
process.env.E2E_LOGO_PORT ??= execFileSync(process.execPath, [
  "-e",
  "const s=require('net').createServer();s.listen(0,'127.0.0.1',()=>{process.stdout.write(String(s.address().port));s.close()})",
]).toString();
export default defineConfig({
  testDir: "./logo-tests",
  testMatch: /logo-browser\.spec\.ts/,
  globalSetup: "./fixtures/logo/setup.ts",
  workers: 1,
  retries: 0,
  timeout: 30_000,
  expect: { timeout: 5_000 },
  outputDir: "./test-results-logo",
  reporter: [
    ["list"],
    ["html", { open: "never", outputFolder: "playwright-report-logo" }],
  ],
  use: {
    ...devices["Desktop Chrome"],
    baseURL: `http://127.0.0.1:${process.env.E2E_LOGO_PORT}`,
    trace: "retain-on-failure",
    screenshot: "only-on-failure",
  },
});
