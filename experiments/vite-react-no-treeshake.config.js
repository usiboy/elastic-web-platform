// 仅用于验证 testB 在 Rollup includePath 高 CPU 热点时禁用 tree-shaking 的因果关系。
// 仍保留 React 插件。此配置会增大产物，不能默认为租户生产策略。
import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

export default defineConfig({
  plugins: [react()],
  build: { rollupOptions: { treeshake: false } },
})
