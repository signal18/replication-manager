import React from 'react'
import { keyframes, Tag } from '@chakra-ui/react'

function TagPill({ size = 'sm', text, type, variant = 'solid', colorScheme, isBlinking }) {
  const blink = keyframes`
    0% { opacity: 1;}
  50% { opacity: 0; }
  100% { opacity: 1; }
`
  const styles = {
    blinking: {
      animation: `${blink} 1s infinite`
    }
  }
  return (
    <Tag size={size} variant={variant} colorScheme={colorScheme} sx={isBlinking && styles.blinking}>
      {text}
    </Tag>
  )
}

export default TagPill
