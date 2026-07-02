import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import viteCompression from 'vite-plugin-compression'
import basicSSL from '@vitejs/plugin-basic-ssl'

// These recharts transitive deps use a lazy cross-chunk class-init pattern that breaks
// when Rollup isolates them into their own chunk (same failure class as the
// react-gauge-component/d3-shape "init_arc is not defined" bug) - keep them out of
// manualChunks so Rollup's default chunking co-locates them correctly with recharts.
const RECHARTS_UNSAFE_CHUNK_DEPS = new Set([
  'internmap', // confirmed cause of "k is not a constructor"
  'clsx',
  'react-smooth',
  'eventemitter3',
  'decimal.js-light',
])

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

            if (
              packageName === 'react-gauge-component' ||
              packageName.startsWith('d3') ||
              RECHARTS_UNSAFE_CHUNK_DEPS.has(packageName)
            ) {
              return
            }

            return `${prefix}/${packageName}`
          }
        } 
      }
    },
  },
})
