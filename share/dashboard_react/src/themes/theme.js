// theme.js
import { Accordion, extendTheme, Tabs } from '@chakra-ui/react'
import { tabsTheme } from './tabsTheme'
import { accordionTheme } from './accordionTheme'
import { menuTheme } from './menuTheme'
import { buttonTheme } from './buttonTheme'

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
      'html, body, #root': {
        height: '100%',
        width: '100%',
        margin: 0,
        padding: 0,
        bg: props.colorMode === 'dark' ? 'primary.dark' : 'primary.light',
        color: props.colorMode === 'dark' ? 'text.dark' : 'text.light'
      },
      'html, body,p, label, span, [role="menu"] > button,  button[class*="accordion"]': {
        color: props.colorMode === 'dark' ? 'text.dark' : 'text.light'
      },
      text: {
        fill: props.colorMode === 'dark' ? 'text.dark !important' : 'text.light !important'
      }
    })
  },
  components: {
    Menu: menuTheme,
    Button: buttonTheme,
    Tabs: tabsTheme,
    Accordion: accordionTheme
  }
})

export const getCurrentTheme = () => {
  return localStorage.getItem('theme')
}

export default theme
