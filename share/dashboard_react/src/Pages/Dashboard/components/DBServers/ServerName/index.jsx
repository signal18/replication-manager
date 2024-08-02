import { Box } from '@chakra-ui/react'
import React from 'react'
import RMButton from '../../../../../components/RMButton'
import styles from './styles.module.scss'

function ServerName({ rowData, isBlocking, as = 'span' }) {
  return (
    <RMButton className={styles.serverName}>
      <Box
        as={as}
        className={isBlocking && styles.text}
        maxWidth='100%'
        whiteSpace='break-spaces'
        textAlign='start'
        overflowWrap='break-word'>{`${rowData.host}:${rowData.port}`}</Box>
    </RMButton>
  )
}

export default ServerName
