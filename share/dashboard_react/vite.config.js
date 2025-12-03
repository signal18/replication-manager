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
        secure: false
      },
      '/graphite': {
        target: 'https://172.18.0.10:10005/',
        secure: false
      }
    }
  },
  plugins: [react(), viteCompression({ algorithm: 'gzip' }), basicSSL()],
  css: {
    preprocessorOptions: {
      scss: {
        silenceDeprecations: ["import", "legacy-js-api", "global-builtin"],
        additionalData: `@import './src/styles/_mixins.scss';
         @import './src/styles/_variables.scss';
         @import './src/styles/_lighttheme.scss'; 
         @import './src/styles/_darktheme.scss';
         @import './src/styles/_global.scss';`
      }
    }
  },
  build: {
    rollupOptions: {
      output: {
        manualChunks(id) {
          if (id.includes('node_modules')) {
            let prefix = 'js'
            if (id.endsWith("css")){
              prefix = 'css'
            }
            return prefix+'/'+id.toString().split('node_modules/')[1].split('/')[0].toString();
          }
        } 
      }
    },
  },
})
