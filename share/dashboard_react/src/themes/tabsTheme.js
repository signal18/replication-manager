import { tabsAnatomy } from '@chakra-ui/anatomy'
import { createMultiStyleConfigHelpers, defineStyle } from '@chakra-ui/react'
import { mode } from '@chakra-ui/theme-tools'
import { useTheme } from '@emotion/react'

const { definePartsStyle, defineMultiStyleConfig } = createMultiStyleConfigHelpers(tabsAnatomy.keys)

// define the base component styles
const baseStyle = definePartsStyle((props) => {
  const theme = useTheme()
  return {
    tab: {
      px: '24px',
      bg: 'transparent',
      borderRadius: '16px 16px 0 0',
      borderBottom: 'none',
      _selected: {
        bg: mode('#3182ce', theme.colors.primary.light)(props),
        color: mode(`#fff`, theme.colors.text.light)(props),
        borderBottom: 'none',
        mb: '-2px',
        px: '32px',
        fontSize: '18px',
        fontWeight: '700'
      },
      _hover: {
        borderColor: mode(`blue.600`, `#fff`)(props)
      },
      _focus: {
        outlineWidth: '0'
      }
    }
  }
})

// export the component theme
export const tabsTheme = defineMultiStyleConfig({ baseStyle })
