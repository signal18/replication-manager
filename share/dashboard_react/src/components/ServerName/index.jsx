import { Box } from '@chakra-ui/react'
import React from 'react'
import RMButton from '../RMButton'
import styles from './styles.module.scss'

function ServerName({ name, isBlocking, as = 'span', className }) {
  return (
    <Box
      as={as}
      className={`${styles.serverName} ${isBlocking && styles.text}  ${className} `}
      maxWidth='100%'
      whiteSpace='break-spaces'
      textAlign='start'
      overflowWrap='break-word'>
      {name}
    </Box>
  )
}

export default ServerName
