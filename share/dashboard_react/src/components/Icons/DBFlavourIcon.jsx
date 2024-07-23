import { IconButton, Tooltip, useColorMode } from '@chakra-ui/react'
import React from 'react'
import { GrMysql } from 'react-icons/gr'
import { SiMariadbfoundation } from 'react-icons/si'

function DBFlavourIcon({ dbFlavor, isBlocking }) {
  const { colorMode } = useColorMode()
  const styles = {
    dbFlavor: {
      backgroundColor: 'transparent',
      svg: {
        fill: isBlocking ? 'white' : colorMode === 'light' ? `blue.900` : `blue.100`,
        fontSize: '2rem'
      },
      _hover: {}
    }
  }
  return (
    <Tooltip label={dbFlavor}>
      {dbFlavor === 'MariaDB' ? (
        <IconButton icon={<SiMariadbfoundation />} sx={styles.dbFlavor} />
      ) : dbFlavor === 'MySQL' ? (
        <IconButton icon={<GrMysql />} sx={styles.dbFlavor} />
      ) : null}
    </Tooltip>
  )
}

export default DBFlavourIcon
