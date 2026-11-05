import { Box, Tooltip } from '@chakra-ui/react'
import React from 'react'
import { HiLockClosed } from 'react-icons/hi'
import { useSelector } from 'react-redux'

// #2268: a global setting set in the configuration file, the command line or the environment.
// The API refuses to change it (immutableGlobalSetting); /api/monitor lists those keys.
export function useGlobalSettingLocked(setting) {
  const immutable = useSelector((state) => state?.globalClusters?.monitor?.immutableSettings)
  return !!setting && Array.isArray(immutable) && immutable.includes(setting)
}

// the small lock shown next to a locked setting's label
export function SettingLockIcon() {
  return (
    <Tooltip label='Locked from system config' hasArrow placement='top'>
      <Box as='span' display='inline-flex' verticalAlign='middle' ml={1} color='orange.400' aria-label='Locked from system config'>
        <HiLockClosed size={14} />
      </Box>
    </Tooltip>
  )
}

// a row label, with the lock when its global setting is locked
export function SettingLabel({ setting, children }) {
  const locked = useGlobalSettingLocked(setting)
  return (
    <>
      {children}
      {locked && <SettingLockIcon />}
    </>
  )
}

// a row value: its controls disabled when its global setting is locked (a disabled fieldset
// disables every input and button inside it)
export function SettingValue({ setting, children }) {
  const locked = useGlobalSettingLocked(setting)
  if (!locked) return children
  return (
    <fieldset disabled style={{ border: 0, margin: 0, padding: 0, minWidth: 0, opacity: 0.6 }}>
      {children}
    </fieldset>
  )
}
