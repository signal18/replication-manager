import { defineStyle, defineStyleConfig } from '@chakra-ui/react'

const baseStyle = defineStyle((props) => {
  return {
    fontWeight: 'bold',
    fontSize: '1.5rem'
  }
})

const outline = defineStyle((props) => {
  const { colorScheme: c } = props
  return {
    border: '2px solid',
    borderColor: `${c}.500`,
    color: `${c}.500`,
    _hover: {
      bg: `${c}.50`
    }
  }
})

const solid = defineStyle((props) => {
  const { colorScheme: c } = props
  return {
    bg: `${c}.500`,
    color: 'white',
    _hover: {
      bg: `${c}.600`
    }
  }
})

const defaultProps = {
  colorScheme: 'blue'
}

export const buttonTheme = defineStyleConfig({ baseStyle, variants: { solid, outline }, defaultProps })
