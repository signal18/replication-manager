import { Box, HStack, Switch } from '@chakra-ui/react'
import React from 'react'
import { useSelector } from 'react-redux'
import MenuOptions from './MenuOptions'
import { setIsDesktop } from '../redux/commonSlice'

function Card({ header, body, showDashboardOptions, width, showSwitch, onSwitchChange }) {
  const {
    common: { theme, isDesktop }
  } = useSelector((state) => state)

  const styles = {
    card: {
      boxShadow: theme === 'light' ? 'rgba(0, 0, 0, 0.16) 0px 1px 4px' : 'rgba(255, 255, 255, 0.16) 1px 0px 7px 0px',
      borderRadius: '16px'
    },
    heading: {
      textAlign: 'center',
      p: '16px',
      bg: theme === 'light' ? `blue.100` : `blue.800`,
      borderTopLeftRadius: '16px',
      borderTopRightRadius: '16px',
      color: '#000',
      fontWeight: 'bold'
    }
  }
  return (
    <Box sx={styles.card} w={width}>
      <HStack size={'sm'} sx={styles.heading}>
        {showSwitch && <Switch colorScheme='blue' onChange={onSwitchChange} />}
        {showDashboardOptions && (
          <MenuOptions
            placement='right-end'
            subMenuPlacement={isDesktop ? 'right-end' : 'bottom'}
            showDashboardOptions={showDashboardOptions}
          />
        )}
        {header}
      </HStack>
      {body}
    </Box>
  )
}

export default Card
