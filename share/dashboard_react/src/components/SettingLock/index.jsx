import { Box, Text, Tooltip } from '@chakra-ui/react'
import React from 'react'
import { HiLockClosed } from 'react-icons/hi'
import { MdLockReset } from 'react-icons/md'
import { useSelector } from 'react-redux'

// #2268: the settings set in the system config (the configuration file, the command line or the
// environment), each with its value there (a secret masked). /api/monitor carries the global ones,
// /api/clusters/{name} the cluster's.

// a global setting set in the system config: the API refuses to change it (immutableGlobalSetting)
export function useGlobalSettingLock(setting) {
  const immutable = useSelector((state) => state?.globalClusters?.monitor?.immutableSettings)
  return lockOf(immutable, setting)
}

// a cluster setting set in the system config: the API changes it, for a test, until the next
// restart, which rolls it back to the system config value
export function useClusterSettingLock(setting) {
  const immutable = useSelector((state) => state?.cluster?.clusterData?.immutableSettings)
  return lockOf(immutable, setting)
}

function lockOf(immutable, setting) {
  if (!setting || !immutable || typeof immutable !== 'object' || !(setting in immutable)) {
    return null
  }
  return { value: immutable[setting] }
}

function LockTooltip({ title, lock }) {
  return (
    <Box>
      <Text fontWeight='bold'>{title}</Text>
      <Text>System config value: {lock.value === '' ? '(empty)' : lock.value}</Text>
    </Box>
  )
}

// the small lock shown next to a global setting's label
export function SettingLockIcon({ lock }) {
  return (
    <Tooltip label={<LockTooltip title='Locked from system config' lock={lock} />} hasArrow placement='top'>
      <Box as='span' display='inline-flex' verticalAlign='middle' ml={1} color='orange.400' aria-label='Locked from system config'>
        <HiLockClosed size={14} />
      </Box>
    </Tooltip>
  )
}

// the rollback lock shown next to a cluster setting's label
export function SettingRollbackIcon({ lock }) {
  return (
    <Tooltip
      label={<LockTooltip title='Set in system config: a change here is rolled back at the next restart' lock={lock} />}
      hasArrow
      placement='top'>
      <Box as='span' display='inline-flex' verticalAlign='middle' ml={1} color='orange.400' aria-label='Rolled back to the system config at restart'>
        <MdLockReset size={16} />
      </Box>
    </Tooltip>
  )
}

// a row label, with the lock of its global setting or the rollback lock of its cluster setting
export function SettingLabel({ setting, clusterSetting, children }) {
  const globalLock = useGlobalSettingLock(setting)
  const clusterLock = useClusterSettingLock(clusterSetting)
  return (
    <>
      {children}
      {globalLock && <SettingLockIcon lock={globalLock} />}
      {clusterLock && <SettingRollbackIcon lock={clusterLock} />}
    </>
  )
}

// a row value: a locked global setting's controls are disabled (a disabled fieldset disables every
// input and button inside it); a cluster setting's stay editable
export function SettingValue({ setting, children }) {
  const lock = useGlobalSettingLock(setting)
  if (!lock) return children
  return (
    <fieldset disabled style={{ border: 0, margin: 0, padding: 0, minWidth: 0, opacity: 0.6 }}>
      {children}
    </fieldset>
  )
}
