import tailwindcss from '@tailwindcss/vite';
import { tanstackRouter } from '@tanstack/router-plugin/vite';
import react from '@vitejs/plugin-react';
import { defineConfig } from 'vitest/config';

// The dev server proxies /api to the backend only when TB_WEB_DEV_API_URL is set
// (for example to the address of the api service); nothing is hard-coded here.
const apiProxyTarget = process.env.TB_WEB_DEV_API_URL;

export default defineConfig({
  plugins: [
    tanstackRouter({ target: 'react', autoCodeSplitting: true }),
    react(),
    tailwindcss(),
  ],
  resolve: {
    tsconfigPaths: true,
  },
  server: {
    host: true,
    ...(apiProxyTarget ? { proxy: { '/api': { target: apiProxyTarget, changeOrigin: true } } } : {}),
  },
  build: {
    sourcemap: false,
  },
  test: {
    environment: 'jsdom',
    include: ['src/**/*.test.{ts,tsx}', 'eslint/**/*.test.ts'],
    setupFiles: ['src/test/setup.ts'],
  },
});
