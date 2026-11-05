import { Box } from '@chakra-ui/react'
import React from 'react'
import styles from './styles.module.scss'

// a settings section in a frame, always with its heading (#2268): the cluster and the global Setup
function SectionFrame({ title, children }) {
  return (
    <Box className={styles.sectionFrame}>
      <Box className={styles.sectionFrameTitle}>{title}</Box>
      <Box className={styles.sectionFrameBody}>{children}</Box>
    </Box>
  )
}

export default SectionFrame
