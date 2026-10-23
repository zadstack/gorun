import { execFileSync, spawn } from "node:child_process";
import {
  mkdtempSync,
  mkdirSync,
  openSync,
  rmSync,
  copyFileSync,
  readFileSync,
} from "node:fs";
import { tmpdir } from "node:os";
import { dirname, resolve, join } from "node:path";
import { fileURLToPath } from "node:url";

export default async function setup() {
  const repo = resolve(dirname(fileURLToPath(import.meta.url)), "../../..");
  const bin = resolve(repo, "e2e/.bin/logo-browser-fixture");
  mkdirSync(dirname(bin), { recursive: true });
  execFileSync("go", ["test", "-c", "-o", bin, "./internal/admin"], {
    cwd: repo,
    stdio: "inherit",
    env: { ...process.env, CGO_ENABLED: "0" },
  });
  const data = mkdtempSync(join(tmpdir(), "gorun-logo-browser-"));
  const env: NodeJS.ProcessEnv = {};
  for (const [key, value] of Object.entries(process.env)) {
    if (!/^(GORUN_|STRIPE_|OPENAI_|ANTHROPIC_|OLLAMA_|OPENROUTER_)/.test(key))
      env[key] = value;
  }
  Object.assign(env, {
    E2E_LOGO_FIXTURE: "1",
    E2E_LOGO_DATA: data,
    E2E_LOGO_PORT: process.env.E2E_LOGO_PORT,
  });
  const child = spawn(
    bin,
    ["-test.run=^TestLogoBrowserFixture$", "-test.timeout=10m"],
    {
      cwd: repo,
      env,
      stdio: ["ignore", openSync(join(data, "server.log"), "a"), "inherit"],
    },
  );
  const base = `http://127.0.0.1:${process.env.E2E_LOGO_PORT}`;
  try {
    for (let tries = 0; ; tries++) {
      if (child.exitCode !== null)
        throw new Error(
          `Logo fixture exited ${child.exitCode}; ${data}/server.log`,
        );
      try {
        if ((await fetch(`${base}/healthz`)).ok) break;
      } catch {}
      if (tries > 150)
        throw new Error(`Logo fixture not ready; ${data}/server.log`);
      await new Promise((done) => setTimeout(done, 100));
    }
  } catch (err) {
    console.error(readFileSync(join(data, "server.log"), "utf8"));
    child.kill("SIGKILL");
    rmSync(data, { recursive: true, force: true });
    throw err;
  }
  process.env.E2E_LOGO_DATA = data;
  return async () => {
    child.kill("SIGTERM");
    await new Promise<void>((done) => {
      const timer = setTimeout(() => {
        child.kill("SIGKILL");
        done();
      }, 3000);
      child.once("exit", () => {
        clearTimeout(timer);
        done();
      });
    });
    mkdirSync(resolve(repo, "e2e/test-results-logo"), { recursive: true });
    copyFileSync(
      join(data, "server.log"),
      resolve(repo, "e2e/test-results-logo/server.log"),
    );
    rmSync(data, { recursive: true, force: true });
  };
}
