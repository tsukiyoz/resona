import { defineConfig } from "@playwright/test";

const externalBaseURL = process.env.PLAYWRIGHT_BASE_URL;

export default defineConfig({
  testDir: "./tests",
  use: {
    baseURL: externalBaseURL ?? "http://127.0.0.1:5173",
    headless: true,
  },
  webServer: externalBaseURL
    ? undefined
    : {
        command: "npm run dev",
        url: "http://127.0.0.1:5173",
        reuseExistingServer: !process.env.CI,
      },
});
