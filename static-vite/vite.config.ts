import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'
import { resolve } from 'node:path'

// base: '/static/' — assets are served via Go's FileServer(staticDir)
// mounted at /static/* (StripPrefix "/static/"). Vite must emit absolute
// /static/assets/… so `GET /static/assets/index-*.js` matches that handler
// // regardless of the shell route ("/" or "/session/{id}"). Vanilla does
// // the same via `href="static/style.css"` + the /session asset rewrite.
export default defineConfig({
  base: '/static/',
  plugins: [react(), tailwindcss()],
  build: {
    // E7: share.html is the second MPA entry (public viewer, bundle-isolated
    // from the app shell per doc 15 §5).
    rollupOptions: {
      input: {
        index: resolve(__dirname, 'index.html'),
        share: resolve(__dirname, 'share.html'),
      },
    },
  },
  server: {
    host: '127.0.0.1',
    port: 5174,
    proxy: {
      '/api': 'http://127.0.0.1:8787',
      '/health': 'http://127.0.0.1:8787',
    },
  },
  test: {
    environment: 'jsdom',
  },
})
