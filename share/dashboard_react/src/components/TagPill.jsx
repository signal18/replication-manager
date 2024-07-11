import React from 'react'
import { Tag } from '@chakra-ui/react'

function TagPill({ size = 'md', text, type, variant = 'solid' }) {
  return (
    <Tag
      size={size}
      variant={variant}
      colorScheme={type === 'success' ? 'green' : type === 'warning' ? 'orange' : 'red'}>
      {text}
    </Tag>
  )
}

export default TagPill
