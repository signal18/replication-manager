import { Badge, Box } from '@chakra-ui/react'
import React from 'react'
import { HiBan, HiExclamation, HiRefresh } from 'react-icons/hi'
import { BiSupport } from "react-icons/bi";
import styles from './styles.module.scss'
import CustomIcon from '../Icons/CustomIcon'
import { clusterHealthColor } from '../../utility/domainPills'

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
// orange when only warnings, green when clean. Both counters sit side by
// side at the top-right — blockers (red bubble, blinking) left of the
// warnings (orange bubble).
export function HealthAlertBadge({ blockers = 0, warnings = 0, text = 'Health', onClick, showText }) {
  const colorScheme = clusterHealthColor(blockers, warnings)
  const icon = blockers > 0 ? HiBan : HiExclamation
  return (
    <Badge
      as={'button'}
      {...(onClick ? { onClick: onClick } : {})}
      colorScheme={colorScheme}
      className={`${styles.badge} ${styles.healthBadge}`}>
      <Box
        as='span'
        className={`alertCount ${styles.alertCount} ${styles.blocker} ${blockers > 0 ? styles.blinking : ''}`}
        style={{ right: '16px' }}>
        {blockers}
      </Box>
      <Box
        as='span'
        className={`alertCount ${styles.alertCount} ${styles.warning}`}>
        {warnings}
      </Box>
      <CustomIcon icon={icon} />
      {showText ? (blockers > 0 ? `${text} Blocker` : text) : ''}
    </Badge>
  )
}

// NetworkBadge is the refresh-toolbar trigger, sized and placed like the
// other alert badges. The monitor loop tick shows as a rolling 0-99 counter
// in a top-left bubble (liveness pulse); green when healthy, red blinking
// when the loop stalls or the peer heartbeat fails.
export function NetworkBadge({ tick, stalled = false, heartbeatFailed = false, text = 'Network', onClick, showText }) {
  const unhealthy = stalled || heartbeatFailed
  const colorScheme = unhealthy ? 'red' : 'green'
  const label = stalled ? 'Stalled' : heartbeatFailed ? 'Heartbeat' : text
  return (
    <Badge
      as={'button'}
      {...(onClick ? { onClick: onClick } : {})}
      colorScheme={colorScheme}
      className={`${styles.badge} ${unhealthy ? styles.blinking : ''}`}>
      {tick !== undefined && (
        <Box
          as='span'
          className={`alertCount ${styles.alertCount} ${unhealthy ? styles.blocker : styles.support}`}
          style={{ right: 'auto', left: '-10px' }}>
          {tick % 100}
        </Box>
      )}
      <CustomIcon icon={HiRefresh} />
      {showText ? label : ''}
    </Badge>
  )
}

// CountPill is the count bubble of the header pills, standing on its own (a log tab's
// count): same round shape, font and white text, in the colour of its domain's pill.
// shade follows the header bubble of that domain (500, or 600 for schema and config).
export function CountPill({ count, colorScheme = 'gray', shade = 500, blink = false }) {
  return (
    <Box
      as='span'
      className={`alertCount ${styles.alertCount} ${styles.inlineCount} ${blink ? styles.blinking : ''}`}
      style={{ background: `var(--chakra-colors-${colorScheme}-${shade})`, color: 'white' }}>
      {count}
    </Box>
  )
}

export default AlertBadge
