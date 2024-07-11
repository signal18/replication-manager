import { Box } from '@chakra-ui/react'
import { useTheme } from '@emotion/react'
import React from 'react'

function NotFound({ text, currentTheme }) {
  const theme = useTheme()
  const styles = {
    container: {
      p: '24px',
      bg: currentTheme === 'light' ? theme.colors.primary.light : theme.colors.primary.dark,
      width: 'fit-content',
      boxShadow:
        currentTheme === 'light'
          ? 'rgba(100, 100, 111, 0.2) 0px 7px 29px 0px'
          : 'rgba(255, 255, 255, 0.2) 0px 7px 29px 0px',
      borderRadius: '16px',
      margin: 'auto'
    }
  }
  return <Box sx={styles.container}>{text}</Box>
}

export default NotFound
