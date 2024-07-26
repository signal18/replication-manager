import React from 'react'
import { Tag } from '@chakra-ui/react'
import styles from './styles.module.scss'

function TagPill({ size = 'sm', text, variant = 'solid', colorScheme, isBlinking }) {
  return (
    <Tag
      size={size}
      variant={variant}
      colorScheme={colorScheme}
      className={`tagpill ${styles.tag} ${isBlinking && styles.blinking}`}>
      {text}
    </Tag>
  )
}

export default TagPill
