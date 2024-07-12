import React from 'react'
import { Tag } from '@chakra-ui/react'

function TagPill({ size = 'md', text, type, variant = 'solid', colorScheme }) {
  return (
    <Tag size={size} variant={variant} colorScheme={colorScheme}>
      {text}
    </Tag>
  )
}

export default TagPill
