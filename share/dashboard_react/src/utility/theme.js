// theme.js
import { extendTheme } from '@chakra-ui/react'

const theme = extendTheme({
  breakpoints: {
    sm: '30em', // 480px
    md: '48em', // 768px
    lg: '62em', // 992px
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
  styles: {
    global: (props) => ({
      // Global styles
      body: {
        bg: props.colorMode === 'dark' ? 'primary.dark' : 'primary.light',
        color: props.colorMode === 'dark' ? 'text.dark' : 'text.light'
      },
      'html, body': {
        height: '100%',
        width: '100%',
        margin: 0,
        padding: 0,
        overflow: 'hidden'
      },
      '#root': {
        height: '100%',
        width: '100%'
      },
      'html, body,p, label': {
        color: props.colorMode === 'dark' ? 'text.dark' : 'text.light'
      }
    })
  },
  components: {
    Button: {
      baseStyle: {
        fontWeight: 'bold'
      },
      sizes: {
        md: {
          fontSize: 'md', // Medium size font
          px: '4', // Horizontal padding
          py: '2' // Vertical padding
        },
        lg: {
          fontSize: 'lg', // Large size font
          px: '6', // Horizontal padding
          py: '3' // Vertical padding
        }
      },
      variants: {
        solid: {
          bg: 'blue.500', // Solid variant background color
          color: 'white', // Solid variant text color
          _hover: {
            bg: 'blue.600' // Solid variant hover background color
          }
        },
        outline: {
          border: '2px solid',
          borderColor: 'blue.500', // Outline variant border color
          color: 'blue.500', // Outline variant text color
          _hover: {
            bg: 'blue.50' // Outline variant hover background color
          }
        }
      }
    }
  }
})

export const getCurrentTheme = () => {
  return localStorage.getItem('theme')
}

export default theme
