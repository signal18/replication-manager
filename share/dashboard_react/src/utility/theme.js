// theme.js
import { extendTheme } from '@chakra-ui/react'

const theme = extendTheme({
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
  styles: {
    global: (props) => ({
      // Global styles
      body: {
        bg: props.colorMode === 'dark' ? 'primary.dark' : 'primary.light',
        color: props.colorMode === 'dark' ? 'text.dark' : 'text.light'
      },
      'html, body':{
        height: '100%',
        width: '100%',
        margin: 0,
        padding: 0,
        overflow: 'hidden',
      },
      '#root': {
        height: '100%',
        width: '100%',
      },
      'html, body,p, label': {
        color: props.colorMode === 'dark' ? 'text.dark' : 'text.light'
      }
    })
  }
})

export const getCurrentTheme = () => {
  return localStorage.getItem('theme')
}

export default theme
