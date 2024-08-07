import { Box, useColorMode } from '@chakra-ui/react'
import { useTheme } from '@emotion/react'
import React from 'react'

function NotFound({ text }) {
  const theme = useTheme()
  const { colorMode } = useColorMode()
  const styles = {
    container: {
      p: '24px',
      bg: colorMode === 'light' ? theme.colors.primary.light : theme.colors.primary.dark,
      width: 'fit-content',
      boxShadow:
        colorMode === 'light'
          ? 'rgba(100, 100, 111, 0.2) 0px 7px 29px 0px'
          : 'rgba(255, 255, 255, 0.2) 0px 7px 29px 0px',
      borderRadius: '16px',
      margin: 'auto'
    }
  }
  return <Box sx={styles.container}>{text}</Box>
}

export default NotFound
