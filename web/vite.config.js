import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'
import tailwindcss from '@tailwindcss/vite'

export default defineConfig({
  plugins: [vue(), tailwindcss()],
  build: {
    outDir: '../internal/server/web/dist',
    emptyOutDir: true,
    chunkSizeWarningLimit: 700,
  },
  server: {
    proxy: { '/api': process.env.SLOWPOKE_API || 'http://localhost:8080' },
  },
})
