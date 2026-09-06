import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'
import { resolve } from 'path'

// https://vitejs.dev/config/
export default defineConfig({
  plugins: [vue()],
  resolve: {
    alias: {
      '@': resolve(__dirname, 'src'),
    },
  },
  clearScreen: false,
  server: {
    port: 1421,
    host: '127.0.0.1',
    strictPort: true,
    // 开发期前端直连独立后端 127.0.0.1:17655。
    // 通过 .env / window.__LAUNCHER_BASE__ 注入 base url
  },
  // Tauri 期望固定的构建产物目录
  build: {
    target: 'es2021',
    outDir: 'dist',
    emptyOutDir: true,
  },
})
