import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import viteCompression from 'vite-plugin-compression';

// https://vitejs.dev/config/
export default defineConfig({
  server: {
    proxy: {
      "/api": {
        target: "http://repman.marie-dev.svc.cloud18:10001/",
        secure: false,
      },
    },
  },
  plugins: [react(),viteCompression({ algorithm: 'gzip' })],
  
})
