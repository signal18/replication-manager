import { Box } from '@chakra-ui/react'
import React from 'react'
import styles from './styles.module.scss'

function NotFound({ text, className }) {
  return <Box className={`${styles.container} ${className}`}>{text}</Box>
}

export default NotFound
