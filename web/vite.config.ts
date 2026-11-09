/// <reference types="vitest/config" />
import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'

// In development Vite serves the UI on 127.0.0.1:7201 and proxies the API to
// the Go backend (started by `make dev` on 7202).
const api = process.env.EVCC_PLANNER_API ?? 'http://127.0.0.1:7202'

export default defineConfig({
  plugins: [vue()],
  server: {
    host: '127.0.0.1',
    port: 7201,
    strictPort: true,
    proxy: { '/api': api, '/healthz': api, '/readyz': api },
  },
  build: { target: 'es2022' },
  test: {
    environment: 'jsdom',
    include: ['src/**/*.test.ts'],
  },
})
