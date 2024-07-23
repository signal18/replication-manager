import { Box, Button, HStack, keyframes, Spinner, Switch, useColorMode } from '@chakra-ui/react'
import React from 'react'
import { useSelector } from 'react-redux'
import MenuOptions from './MenuOptions'

function Card({
  header,
  body,
  headerAction,
  menuOptions,
  buttonText,
  buttonColorScheme,
  isButtonBlinking = false,
  isLoading,
  loadingText,
  onClick,
  width
}) {
  const {
    common: { isDesktop }
  } = useSelector((state) => state)
  const { colorMode } = useColorMode()

//   const blink = keyframes`
//   0% { opacity: 1;}
//   50% { opacity: 0; }
//   100% { opacity: 1; }
// `


  const blink = keyframes`
   0% { background-color: red; }
  50% { background-color: #2b6cb0; }
  100% { background-color: red; }
`
  const styles = {
    card: {
      borderRadius: '16px',
      border: '1px solid',
      borderColor: colorMode === 'light' ? 'blue.100' : 'blue.800'
    },
    heading: {
      textAlign: 'center',
      p: '4px 8px',
      bg: colorMode === 'light' ? `blue.100` : `blue.800`,
      borderTopLeftRadius: '16px',
      borderTopRightRadius: '16px',
      color: '#000',
      fontWeight: 'bold'
    },
    blinking: {
      animation: `${blink} 1s infinite`
    }
  }

  return (
    <Box sx={styles.card} w={width}>
      <HStack size={'sm'} sx={styles.heading}>
        {headerAction === 'menu' && (
          <MenuOptions
            placement='right-end'
            options={menuOptions}
            subMenuPlacement={isDesktop ? 'right-end' : 'bottom'}
          />
        )}
        {headerAction === 'button' && (
          <Button
            sx={isButtonBlinking ? styles.blinking : ''}
            colorScheme={buttonColorScheme}
            size='sm'
            onClick={onClick}
            isLoading={isLoading}
            loadingText={loadingText}>
            {buttonText}
          </Button>
        )}
        {headerAction !== 'button' && isLoading && <Spinner label={loadingText} speed='1s' />}
        {header}
      </HStack>
      {body}
    </Box>
  )
}

export default Card
