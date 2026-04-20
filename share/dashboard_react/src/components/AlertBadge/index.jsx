import { Badge, Box } from '@chakra-ui/react'
import React from 'react'
import { HiBan, HiExclamation } from 'react-icons/hi'
import { BiSupport } from "react-icons/bi";
import styles from './styles.module.scss'
import CustomIcon from '../Icons/CustomIcon'

function AlertBadge({
  isBlocking = false,
  count,
  text,
  onClick,
  showText,
  isSupport = false,
  isConnect = true,
  // Optional overrides — when provided, bypass the isBlocking/isSupport logic
  colorScheme: colorSchemeProp,
  icon: iconProp,
  // Optional inline style applied to the count bubble (overrides class-based colour)
  bubbleStyle,
}) {
  const resolvedColorScheme =
    colorSchemeProp !== undefined
      ? colorSchemeProp
      : isBlocking ? 'red' : isSupport ? (isConnect ? 'green' : 'red') : 'orange'

  const resolvedIcon =
    iconProp !== undefined
      ? iconProp
      : isBlocking ? HiBan : isSupport ? BiSupport : HiExclamation

  const showBubble =
    isBlocking || (!isBlocking && !isSupport) || (isSupport && isConnect)

  const bubbleClassName = `alertCount ${styles.alertCount} ${
    isBlocking ? styles.blocker : isSupport ? styles.support : styles.warning
  } ${isBlocking && count > 0 ? styles.blinking : ''}`

  return (
    <Badge
      as={'button'}
      {...(onClick ? { onClick: onClick } : {})}
      colorScheme={resolvedColorScheme}
      className={styles.badge}>
      {showBubble && (
        <Box
          as='span'
          className={bubbleStyle ? `alertCount ${styles.alertCount}` : bubbleClassName}
          style={bubbleStyle}>
          {count}
        </Box>
      )}
      <CustomIcon icon={resolvedIcon} />
      {showText ? text : ''}
    </Badge>
  )
}

export default AlertBadge
