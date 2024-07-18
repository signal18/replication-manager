import { Box, Button } from '@chakra-ui/react'
import React from 'react'

function ServerName({ rowData }) {
  const styles = {
    serverName: {
      backgroundColor: 'transparent',
      display: 'flex',
      padding: '0',
      width: '100%',
      _hover: {}
    }
  }
  return (
    <Button type='button' sx={styles.serverName}>
      <Box
        as='span'
        maxWidth='100%'
        whiteSpace='break-spaces'
        textAlign='start'
        overflowWrap='break-word'>{`${rowData.host}:${rowData.port}`}</Box>
    </Button>
  )
}

export default ServerName
