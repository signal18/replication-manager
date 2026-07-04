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
  blink = false,
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
    (isBlocking || (!isBlocking && !isSupport) || (isSupport && isConnect)) && count !== '' && count !== undefined && count !== null

  const bubbleClassName = `alertCount ${styles.alertCount} ${
    isBlocking ? styles.blocker : isSupport ? styles.support : styles.warning
  } ${(isBlocking && count > 0) || blink ? styles.blinking : ''}`

  return (
    <Badge
      as={'button'}
      {...(onClick ? { onClick: onClick } : {})}
      colorScheme={resolvedColorScheme}
      className={`${styles.badge} ${blink ? styles.blinking : ''}`}>
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

// HealthAlertBadge is the single health button: red when blockers exist,
// orange when only warnings, green when clean. Both counters are shown on
// the button — blockers top-right (red bubble, blinking), warnings
// bottom-right (orange bubble).
export function HealthAlertBadge({ blockers = 0, warnings = 0, text = 'Health', onClick, showText }) {
  const colorScheme = blockers > 0 ? 'red' : warnings > 0 ? 'orange' : 'green'
  const icon = blockers > 0 ? HiBan : HiExclamation
  return (
    <Badge
      as={'button'}
      {...(onClick ? { onClick: onClick } : {})}
      colorScheme={colorScheme}
      className={styles.badge}>
      <Box
        as='span'
        className={`alertCount ${styles.alertCount} ${styles.blocker} ${blockers > 0 ? styles.blinking : ''}`}>
        {blockers}
      </Box>
      <Box
        as='span'
        className={`alertCount ${styles.alertCount} ${styles.warning}`}
        style={{ top: 'auto', bottom: '-10px' }}>
        {warnings}
      </Box>
      <CustomIcon icon={icon} />
      {showText ? (blockers > 0 ? `${text} Blocker` : text) : ''}
    </Badge>
  )
}

export default AlertBadge
