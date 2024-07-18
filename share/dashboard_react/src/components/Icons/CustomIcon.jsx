import { Icon, useColorMode } from '@chakra-ui/react'
import React from 'react'

function CustomIcon({ icon, color, fontSize = '1.5rem' }) {
  const { colorMode } = useColorMode()

  const styles = {
    icon: {
      fontSize: fontSize
    },
    green: {
      fill: colorMode === 'light' ? 'green' : 'lightgreen'
    },
    red: { fill: 'red' },

    orange: {
      fill: 'orange'
    }
  }
  return <Icon sx={{ ...styles.icon, ...styles[color] }} as={icon} />
}

export default CustomIcon
