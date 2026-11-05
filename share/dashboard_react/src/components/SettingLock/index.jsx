import { Box, Flex } from '@chakra-ui/react'
import React, { useState } from 'react'
import { HiLockClosed, HiLockOpen } from 'react-icons/hi'
import { useSelector } from 'react-redux'
import Markdown from 'react-markdown'
import remarkGfm from 'remark-gfm'
import RMIconButton from '../RMIconButton'
import CommonModal from '../Modals/CommonModal'
import modalStyles from '../Modals/styles.module.scss'

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
  return { setting, value: immutable[setting] }
}

// the lock button, the help button's twin (same RMIconButton, size and ghost style, the same
// info window), so that both read the same way in a row
function LockButton({ icon, title, text, lock }) {
  const [isOpen, setIsOpen] = useState(false)
  const value = lock.value === '' ? '_(empty)_' : '`' + lock.value + '`'
  const content = `**${title}**\n\n${text}\n\n**System config value:** ${value}\n\nConfig: \`${lock.setting}\``
  return (
    <>
      <RMIconButton
        icon={icon}
        tooltip={title}
        onClick={() => setIsOpen(true)}
        iconFontsize='1rem'
        variant='ghost'
        colorScheme='orange'
        style={{ minWidth: '1.5rem', height: '1.5rem' }}
      />
      <CommonModal
        isOpen={isOpen}
        closeModal={() => setIsOpen(false)}
        title={title}
        body={<Box className={modalStyles.infoTooltip}><Markdown remarkPlugins={[remarkGfm]}>{content}</Markdown></Box>}
        size='xl'
      />
    </>
  )
}

// a lock for a setting the instance's plan does not include (#2268): the same button and window as
// the system config locks, with what unlocks it
export function PlanLockButton({ title, text }) {
  const [isOpen, setIsOpen] = useState(false)
  return (
    <>
      <RMIconButton
        icon={HiLockClosed}
        tooltip={title}
        onClick={() => setIsOpen(true)}
        iconFontsize='1rem'
        variant='ghost'
        colorScheme='orange'
        style={{ minWidth: '1.5rem', height: '1.5rem' }}
      />
      <CommonModal
        isOpen={isOpen}
        closeModal={() => setIsOpen(false)}
        title={title}
        body={<Box className={modalStyles.infoTooltip}><Markdown remarkPlugins={[remarkGfm]}>{text}</Markdown></Box>}
        size='xl'
      />
    </>
  )
}

// a row label, with the lock of its global setting or the rollback lock of its cluster setting,
// on the right of the label cell, apart from its text
export function SettingLabel({ setting, clusterSetting, children }) {
  const globalLock = useGlobalSettingLock(setting)
  const clusterLock = useClusterSettingLock(clusterSetting)
  if (!globalLock && !clusterLock) return children
  return (
    <Flex align='center' justify='space-between' gap={2} w='100%'>
      <Box>{children}</Box>
      {globalLock && (
        <LockButton
          icon={HiLockClosed}
          title='Locked from system config'
          text='This setting is set in the system config (the configuration file, the command line or the environment). The API refuses to change it: change it there.'
          lock={globalLock}
        />
      )}
      {clusterLock && (
        <LockButton
          icon={HiLockOpen}
          title='Rolled back at restart'
          text='This setting is set in the system config. A change here applies at once, for a test, and the next restart rolls it back to the system config value.'
          lock={clusterLock}
        />
      )}
    </Flex>
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
