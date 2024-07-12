import { defineStyle, defineStyleConfig } from '@chakra-ui/react'

const baseStyle = defineStyle((props) => {
  return {
    fontWeight: 'bold',
    fontSize: '1.5rem'
  }
})

const outline = defineStyle({
  border: '2px solid',
  borderColor: 'blue.500',
  color: 'blue.500',
  _hover: {
    bg: 'blue.50'
  }
})

const solid = defineStyle({
  bg: 'blue.500',
  color: 'white',
  _hover: {
    bg: 'blue.600'
  }
})

export const buttonTheme = defineStyleConfig({ baseStyle, variants: { solid, outline } })
