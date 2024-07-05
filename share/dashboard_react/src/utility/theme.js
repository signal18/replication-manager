// theme.js
import { extendTheme } from '@chakra-ui/react'

const theme = extendTheme({
  colors: {
    primary: {
      light: '#6CB4EE',
      dark: '#3C82C3'
    },
    background: {
      light: '#FFFFFF',
      dark: '#1A202C'
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
  styles: {
    global: {
      // Global styles
      body: {
        bg: 'background.light',
        color: 'text.light'
      }
    }
  }
})

export const getCurrentTheme = () => {
  return localStorage.getItem('theme')
}

export default theme
