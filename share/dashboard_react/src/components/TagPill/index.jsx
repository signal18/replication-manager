import React from 'react'
import { Tag } from '@chakra-ui/react'
import styles from './styles.module.scss'

function TagPill({ size = 'sm', text, variant = 'solid', customColorScheme = '', colorScheme, isBlinking }) {
  return (
    <Tag
      size={size}
      variant={variant}
      colorScheme={!customColorScheme && colorScheme}
      {...(customColorScheme ? { bg: customColorScheme } : {})}
      className={`tagpill ${styles.tag}   ${isBlinking && styles.blinking}`}>
      {text}
    </Tag>
  )
}

export default TagPill
