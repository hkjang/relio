import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

export default defineConfig({
  plugins: [react()],
  build: {
    outDir: '../internal/webui/dist',
    emptyOutDir: true,
    sourcemap: false,
    assetsInlineLimit: 4096,
  },
  server: {
    port: 5173,
    proxy: { '/api': 'http://localhost:8080', '/mcp': 'http://localhost:8080' },
  },
})
