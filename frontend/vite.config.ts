import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'

// In dev, /api is proxied to the Go server; in Docker, nginx does the same.
export default defineConfig({
  plugins: [vue()],
  server: {
    port: 5173,
    proxy: { '/api': process.env.API_URL ?? 'http://localhost:8080' },
  },
})
