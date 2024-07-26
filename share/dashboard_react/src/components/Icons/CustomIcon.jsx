import { Icon } from '@chakra-ui/react'
import React from 'react'
import { useTheme } from '../../ThemeProvider'

function CustomIcon({ icon, color, fontSize = '1.5rem', fill }) {
  const { theme } = useTheme()

  const styles = {
    icon: {
      fontSize: fontSize
    },
    green: {
      fill: theme === 'light' ? 'green' : 'lightgreen'
    },
    red: { fill: 'red' },

    orange: {
      fill: 'orange'
    }
  }
  return <Icon sx={{ ...styles.icon, ...styles[color] }} as={icon} fill={fill} />
}

export default CustomIcon
