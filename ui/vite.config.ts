import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';
import { PORTAL_BASE } from './src/portalBase.ts';

// https://vite.dev/config/
export default defineConfig({
  plugins: [react()],
  base: `${PORTAL_BASE}/`,
  server: {
    proxy: {
      '/api': {
        target: 'http://127.0.0.1:8000',
        changeOrigin: true,
      },
    },
  },
});
