import { menuAnatomy } from '@chakra-ui/anatomy'
import { createMultiStyleConfigHelpers } from '@chakra-ui/react'

const { definePartsStyle, defineMultiStyleConfig } = createMultiStyleConfigHelpers(menuAnatomy.keys)

const baseStyle = definePartsStyle((props) => {
  return {
    button: {
      bg: props.colorMode === 'light' ? 'blue.100' : 'blue.800'
    }
    // button: {
    //   bg: props.colorMode === 'light' ? 'blue.100' : 'blue.800',
    //   borderTopLeftRadius: '16px',
    //   borderTopRightRadius: '16px',
    //   fontWeight: 'bold',
    //   _hover: {
    //     bg: props.colorMode === 'light' ? 'blue.100' : 'blue.800'
    //   }
    // },
    // panel: {
    //   borderBottomLeftRadius: '16px',
    //   borderBottomRightRadius: '16px',
    //   border: '1px solid',
    //   borderColor: props.colorMode === 'light' ? 'blue.100' : 'blue.800'
    // },
    // icon: {
    //   fontSize: '1.5rem'
    // }
  }
})
export const menuTheme = defineMultiStyleConfig({ baseStyle })
