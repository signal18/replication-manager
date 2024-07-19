import { Box, Button, HStack, Spinner, Switch, useColorMode } from '@chakra-ui/react'
import React from 'react'
import { useSelector } from 'react-redux'
import MenuOptions from './MenuOptions'

function Card({ header, body, headerAction, menuOptions, buttonText, isLoading, loadingText, onClick, width }) {
  const {
    common: { isDesktop }
  } = useSelector((state) => state)
  const { colorMode } = useColorMode()
  const styles = {
    card: {
      borderRadius: '16px',
      border: '1px solid',
      borderColor: colorMode === 'light' ? 'blue.100' : 'blue.800'
    },
    heading: {
      textAlign: 'center',
      p: '8px',
      bg: colorMode === 'light' ? `blue.100` : `blue.800`,
      borderTopLeftRadius: '16px',
      borderTopRightRadius: '16px',
      color: '#000',
      fontWeight: 'bold'
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
          <Button variant='outline' size='sm' onClick={onClick} isLoading={isLoading} loadingText={loadingText}>
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
