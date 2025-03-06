import { Badge, Box } from '@chakra-ui/react'
import React from 'react'
import { HiBan, HiExclamation } from 'react-icons/hi'
import { BiSupport } from "react-icons/bi";
import styles from './styles.module.scss'
import CustomIcon from '../Icons/CustomIcon'

function AlertBadge({ isBlocking = false, count, text, onClick, showText, isSupport = false, isConnect = true }) {
  return (
    <Badge
      as={'button'}
      {...(onClick ? { onClick: onClick } : {})}
      colorScheme={isBlocking ? 'red' : isSupport ? (isConnect ? 'green' : 'red') : 'orange'}
      className={styles.badge}>
      { (isBlocking || (!isBlocking && !isSupport) || (isSupport && isConnect)) && (
        <Box
          as='span'
          className={`alertCount ${styles.alertCount} ${isBlocking ? styles.blocker : isSupport ? styles.support : styles.warning} ${isBlocking && count > 0 ? styles.blinking : {}}`}>
          {count}
        </Box>
      )}
      <CustomIcon icon={isBlocking ? HiBan : isSupport ? BiSupport :  HiExclamation} />
      {showText ? text : ''}
    </Badge>
  )
}

export default AlertBadge
