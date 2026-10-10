import { defineConfig, devices } from "@playwright/test"

// End-to-end checks of the UI against the mock backend (src/mock), built in
// mock mode and served by `vite preview`, so the page runs under the
// production CSP of index.html. Chromium only (WebView2 is Chromium; macOS's
// WebKit is not covered here).
const port = Number(process.env.E2E_PORT) || 9246
const baseURL = process.env.E2E_BASE_URL || `http://127.0.0.1:${port}/`

export default defineConfig({
  testDir: "e2e",
  outputDir: "test-results",
  fullyParallel: true,
  forbidOnly: !!process.env.CI,
  retries: 0,
  // ubuntu-latest has 4 cores; the default (half) doubles the run time.
  workers: process.env.CI ? 4 : undefined,
  reporter: process.env.CI ? [["list"], ["html", { open: "never", outputFolder: "playwright-report" }]] : "list",
  use: {
    baseURL,
    viewport: { width: 1100, height: 720 },
    // Fewer transitions in flight while axe reads colours.
    reducedMotion: "reduce",
    trace: "retain-on-failure",
  },
  projects: [
    {
      name: "chromium",
      use: {
        ...devices["Desktop Chrome"],
        viewport: { width: 1100, height: 720 },
        // A local Chromium (PLAYWRIGHT_CHROMIUM) instead of the downloaded one.
        launchOptions: process.env.PLAYWRIGHT_CHROMIUM ? { executablePath: process.env.PLAYWRIGHT_CHROMIUM } : {},
      },
    },
  ],
  webServer: process.env.E2E_BASE_URL
    ? undefined
    : {
        command: `npm run build:mock && npx vite preview --mode mock --outDir dist-mock --host 127.0.0.1 --port ${port} --strictPort`,
        url: baseURL,
        reuseExistingServer: !process.env.CI,
        timeout: 180_000,
      },
})
