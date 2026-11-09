import { defineConfig, devices } from '@playwright/test'
import { mkdtempSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'

// Browser tests start the real Go binary (built by `make build`) in demo
// mode with a fixed clock, stubbed integrations, and a throwaway database.
const port = Number(process.env.E2E_PORT ?? 7299)
if (!process.env.E2E_DATA_DIR) {
  process.env.E2E_DATA_DIR = mkdtempSync(join(tmpdir(), 'evcc-planner-e2e-'))
  console.log(`e2e server log: ${join(process.env.E2E_DATA_DIR, 'server.log')}`)
}

export default defineConfig({
  testDir: 'e2e',
  outputDir: 'test-results',
  // Tests share one server and reset its demo data, so they run serially.
  workers: 1,
  fullyParallel: false,
  forbidOnly: !!process.env.CI,
  reporter: [['list'], ['html', { open: 'never', outputFolder: 'playwright-report' }]],
  use: {
    baseURL: `http://127.0.0.1:${port}`,
    // A non-Dutch browser zone proves the UI shows planner (Amsterdam) times.
    timezoneId: 'America/New_York',
    locale: 'en-GB',
    screenshot: 'only-on-failure',
    trace: 'retain-on-failure',
  },
  projects: [
    { name: 'desktop', use: { ...devices['Desktop Chrome'] } },
    { name: 'mobile', use: { ...devices['Pixel 7'] } },
  ],
  webServer: {
    // The server log goes to <data dir>/server.log (path printed when the tests start).
    command:
      'exec ../bin/evcc-planner -config ../config.yaml -local /dev/null -env /dev/null' +
      ' >> "$EVCC_PLANNER_DATA_DIR/server.log" 2>&1',
    url: `http://127.0.0.1:${port}/readyz`,
    reuseExistingServer: false,
    timeout: 30_000,
    stdout: 'ignore',
    stderr: 'pipe',
    env: {
      EVCC_PLANNER_DEMO: 'true',
      EVCC_PLANNER_FIXED_TIME: '2026-11-09T19:00:00+01:00',
      EVCC_PLANNER_LISTEN: `127.0.0.1:${port}`,
      EVCC_PLANNER_DATA_DIR: process.env.E2E_DATA_DIR,
      EVCC_PLANNER_WEB_DIR: 'dist',
    },
  },
})
