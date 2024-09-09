import { Box } from '@chakra-ui/react'
import React from 'react'
import RMButton from '../../../../../components/RMButton'
import styles from './styles.module.scss'

function ServerName({ name, isBlocking, as = 'span', className }) {
  return (
    <RMButton className={styles.serverName}>
      <Box
        as={as}
        className={isBlocking && styles.text}
        maxWidth='100%'
        whiteSpace='break-spaces'
        textAlign='start'
        overflowWrap='break-word'>
        {name}
      </Box>
    </RMButton>
  )
}

export default ServerName
