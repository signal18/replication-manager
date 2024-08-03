// theme.js
import { extendTheme } from '@chakra-ui/react'

const theme = extendTheme({
  breakpoints: {
    sm: '30em', // 480px
    md: '48em', // 768px
    lg: '64em', // 1024px
    xl: '80em' // 1280px
  },
  colors: {
    primary: {
      light: '#eff2fe',
      dark: '#131A34'
    },

    text: {
      light: '#333333',
      dark: '#FFFFFF'
    }
  },
  config: {
    initialColorMode: 'light', // Set initial color mode here
    useSystemColorMode: false // Optional: enables automatic switching based on system preferences
  },
  components: {
    // Menu: menuTheme
  }
})

export const getCurrentTheme = () => {
  return localStorage.getItem('theme')
}

export default theme
