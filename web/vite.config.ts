import tailwindcss from '@tailwindcss/vite';
import { defineConfig } from 'vite';

// In development, `pnpm dev` serves the UI and proxies API calls to the Go
// server. In production the Go server embeds dist/ and serves everything.
export default defineConfig({
  plugins: [tailwindcss()],
  server: {
    proxy: {
      '/api': 'http://localhost:8080',
      '/healthz': 'http://localhost:8080',
    },
  },
  build: {
    outDir: 'dist',
    emptyOutDir: true,
    chunkSizeWarningLimit: 1500,
  },
});
