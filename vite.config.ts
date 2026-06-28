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
    port: 1420,
    strictPort: true,
    // Tauri 壳会把 sidecar 跑在固定端口；开发期前端直连 127.0.0.1:17654
    // 通过 .env / window.__LAUNCHER_BASE__ 注入 base url
  },
  // Tauri 期望固定的构建产物目录
  build: {
    target: 'es2021',
    outDir: 'dist',
    emptyOutDir: true,
  },
})
