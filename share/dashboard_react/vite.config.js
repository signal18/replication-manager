import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import viteCompression from 'vite-plugin-compression'
import basicSSL from '@vitejs/plugin-basic-ssl'

// https://vitejs.dev/config/
export default defineConfig({
  server: {
    https: true,
    proxy: {
      '/api': {
        target: 'https://172.18.0.10:10005/',
        secure: false,
        ws: true,
        rewriteWsOrigin: true,
      },
      '/graphite': {
        target: 'https://172.18.0.10:10005/',
        secure: false
      }
    },
  },
  plugins: [react(), viteCompression({ algorithm: 'gzip' }), basicSSL()],
  css: {
    preprocessorOptions: {
      scss: {
        silenceDeprecations: ["import", "legacy-js-api", "global-builtin"],
        additionalData: `@import '/src/styles/_mixins.scss';
         @import '/src/styles/_variables.scss';`
      }
    }
  },
  build: {
    chunkSizeWarningLimit: 800,
    rollupOptions: {
      output: {
        manualChunks(id) {
          if (id.includes('node_modules')) {
            const prefix = id.endsWith('css') ? 'css' : 'js'
            const pkgPath = id.toString().split('node_modules/')[1]
            if (!pkgPath) return

            const parts = pkgPath.split('/')
            const packageName = parts[0].startsWith('@') && parts.length > 1
              ? `${parts[0]}/${parts[1]}`
              : parts[0]

            return `${prefix}/${packageName}`
          }
        } 
      }
    },
  },
})
