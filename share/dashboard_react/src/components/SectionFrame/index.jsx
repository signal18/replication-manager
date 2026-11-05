import { Box, Text } from '@chakra-ui/react'
import React from 'react'
import styles from './styles.module.scss'

// a settings section in a frame, always with its heading (#2268): the cluster and the global Setup.
// isEmpty: nothing to set here, the frame says why (emptyText) instead of showing an empty table
function SectionFrame({ title, children, isEmpty = false, emptyText = 'Nothing to set here.' }) {
  return (
    <Box className={styles.sectionFrame}>
      <Box className={styles.sectionFrameTitle}>{title}</Box>
      <Box className={styles.sectionFrameBody}>
        {isEmpty ? <Text className={styles.sectionFrameEmpty}>{emptyText}</Text> : children}
      </Box>
    </Box>
  )
}

export default SectionFrame
