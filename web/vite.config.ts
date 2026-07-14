import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'

// Built assets are embedded by the Go server at /admin/.
export default defineConfig({
  plugins: [react(), tailwindcss()],
  base: '/admin/',
  build: {
    outDir: '../internal/webui/dist',
    emptyOutDir: true,
    // No production source map: it is embedded and served from /admin, exposing
    // the full TypeScript source (~1.4 MB). Not needed for an internal admin UI.
    sourcemap: false,
  },
  server: {
    port: 5173,
    proxy: {
      // Dev: Vite proxies API/auth to a local semidx serve.
      '/admin/api': 'http://127.0.0.1:8080',
      '/admin/login': 'http://127.0.0.1:8080',
      '/admin/logout': 'http://127.0.0.1:8080',
      '/api': 'http://127.0.0.1:8080',
      '/healthz': 'http://127.0.0.1:8080',
    },
  },
})
