import { Badge, Box } from '@chakra-ui/react'
import React from 'react'
import { HiBan, HiExclamation } from 'react-icons/hi'
import styles from './styles.module.scss'
import CustomIcon from '../Icons/CustomIcon'

function AlertBadge({ isBlocking = false, count, text, onClick, showText }) {
  return (
    <Badge
      as={'button'}
      {...(onClick ? { onClick: onClick } : {})}
      colorScheme={isBlocking ? 'red' : 'orange'}
      className={styles.badge}>
      <Box
        as='span'
        className={`alertCount ${styles.alertCount} ${isBlocking ? styles.blocker : styles.warning} ${isBlocking && count > 0 ? styles.blinking : {}}`}>
        {count}
      </Box>
      <CustomIcon icon={isBlocking ? HiBan : HiExclamation} />
      {showText ? text : ''}
    </Badge>
  )
}

export default AlertBadge
