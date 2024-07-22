import { Badge, Flex, Box, keyframes, Icon } from '@chakra-ui/react'
import React from 'react'
import { HiBan, HiExclamation } from 'react-icons/hi'

function AlertBadge({ isBlocking = false, count, text, onClick, showText }) {
  const blink = keyframes`
  0% { opacity: 1; }
  50% { opacity: 0; }
  100% { opacity: 1; }
`

  const styles = {
    badge: {
      borderRadius: '8px',
      padding: '4px',
      position: 'relative',
      paddingRight: '16px',
      paddingLeft: '8px',
      display: 'flex',
      alignItems: 'center',
      gap: '4px'
    },
    alertCount: {
      width: '24px',
      height: '24px',
      borderRadius: '50%',
      backgroundColor: 'white',
      position: 'absolute',
      right: '-10px',
      top: '-10px',
      display: 'flex',
      alignItems: 'center',
      justifyContent: 'center'
    },
    blocker: {
      background: 'red',
      color: 'white'
    },
    blinking: {
      animation: `${blink} 1s infinite`
    },
    warning: {
      background: 'orange.500',
      color: 'white'
    }
  }
  return (
    <Badge
      as={'button'}
      {...(onClick ? { onClick: onClick } : {})}
      colorScheme={isBlocking ? 'red' : 'orange'}
      sx={{ ...styles.badge, ...(isBlocking ? styles.blockerBadge : {}) }}>
      <Box
        as='span'
        sx={{
          ...styles.alertCount,
          ...(isBlocking ? styles.blocker : styles.warning),
          ...(isBlocking && count > 0 ? styles.blinking : {})
        }}>
        {count}
      </Box>
      <Icon as={isBlocking ? HiBan : HiExclamation} fontSize={'1rem'} />
      {showText ? text : ''}
    </Badge>
  )
}

export default AlertBadge
