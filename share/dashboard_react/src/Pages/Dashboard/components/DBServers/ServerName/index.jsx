import { Box } from '@chakra-ui/react'
import React from 'react'
import Button from '../../../../../components/Button'
import styles from './styles.module.scss'

function ServerName({ rowData, isBlocking }) {
  return (
    <Button className={styles.serverName}>
      <Box
        as='span'
        className={isBlocking && styles.text}
        maxWidth='100%'
        whiteSpace='break-spaces'
        textAlign='start'
        overflowWrap='break-word'>{`${rowData.host}:${rowData.port}`}</Box>
    </Button>
  )
}

export default ServerName
