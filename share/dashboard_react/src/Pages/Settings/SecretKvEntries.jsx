import {
  Badge,
  Flex,
  FormControl,
  FormLabel,
  Input,
  Modal,
  ModalBody,
  ModalCloseButton,
  ModalContent,
  ModalFooter,
  ModalHeader,
  ModalOverlay,
  Stack,
  Switch,
  Text,
  Tooltip
} from '@chakra-ui/react'
import React, { useCallback, useEffect, useState } from 'react'
import RMButton from '../../components/RMButton'
import ConfirmModal from '../../components/Modals/ConfirmModal'
import parentStyles from '../../components/Modals/styles.module.scss'
import { clusterService } from '../../services/clusterService'
import { useTheme } from '../../ThemeProvider'
import { NAME_PATTERN, SettingField } from './ArchiveListSettings'

// The cluster's vault entries (#2227): each enabled entry gets every version of every secret of
// the cluster at <mount>/<path-prefix>/<cluster>/databases|proxies|apps|archives/<name>/<key>.
// Secrets go in through the form and never come back (secretsSet).

const SETTING_LABELS = {
  address: 'Address',
  mount: 'Mount (KV v2)',
  'path-prefix': 'Path prefix',
  mode: 'Mode',
  auth: 'Sign-in',
  'role-id': 'Role id',
  'secret-id': 'Secret id'
}

// the state pill of an entry: off, behind (WARN0269) with its reason, or on
function statePill(entry, state, variant) {
  if (!entry.enabled) return <Badge colorScheme='gray' variant={variant}>Off</Badge>
  if (state?.behind) {
    return (
      <Tooltip label={state.behind} hasArrow>
        <Badge colorScheme='orange' variant={variant}>Behind</Badge>
      </Tooltip>
    )
  }
  return <Badge colorScheme='green' variant={variant}>On</Badge>
}

function SecretKvFormModal({ isOpen, closeModal, manifest, entry, onSave }) {
  const { theme } = useTheme()
  const isEdit = !!entry
  const [name, setName] = useState(entry?.name || '')
  const [values, setValues] = useState({ ...(entry?.settings || {}) })
  const [error, setError] = useState('')
  const [isSaving, setIsSaving] = useState(false)
  const settings = manifest?.settings || []

  const handleSave = async () => {
    setError('')
    if (!isEdit && !new RegExp(NAME_PATTERN).test(name)) {
      setError('Name: lowercase letters, digits, - and _ only')
      return
    }
    const out = {}
    const unset = []
    settings.forEach((st) => {
      const v = values[st.name]
      if (v === '' || v === null || v === undefined) {
        if (isEdit && entry.settings && st.name in entry.settings) unset.push(st.name)
        return
      }
      out[st.name] = String(v)
    })
    setIsSaving(true)
    const err = await onSave({ name: isEdit ? entry.name : name, plugin: manifest?.name, settings: out, unset }, isEdit)
    setIsSaving(false)
    if (err) setError(err)
  }

  return (
    <Modal isOpen={isOpen} onClose={closeModal} size='lg'>
      <ModalOverlay />
      <ModalContent className={theme === 'light' ? parentStyles.modalLightContent : parentStyles.modalDarkContent}>
        <ModalHeader>{isEdit ? `Edit ${entry.name}` : 'Add a vault'}</ModalHeader>
        <ModalCloseButton />
        <ModalBody>
          <Stack spacing='3'>
            {!isEdit && (
              <FormControl>
                <FormLabel fontSize='sm' mb='1'>Name</FormLabel>
                <Input size='sm' value={name} placeholder='vault-own' onChange={(e) => setName(e.target.value.trim())} />
              </FormControl>
            )}
            {settings.map((st) => (
              <FormControl key={st.name}>
                <FormLabel fontSize='sm' mb='1'>{SETTING_LABELS[st.name] || st.name}</FormLabel>
                <SettingField st={st} value={values[st.name]} onChange={(v) => setValues((prev) => ({ ...prev, [st.name]: v }))}
                  secretSet={(entry?.secretsSet || []).includes(st.name)} />
              </FormControl>
            ))}
            <Text fontSize='sm' opacity='0.7'>
              The secrets are written under the mount, at path-prefix/cluster. repman never deletes what the vault holds.
            </Text>
            {error && <Text color={theme === 'dark' ? 'red.300' : 'red.500'} fontSize='sm'>{error}</Text>}
          </Stack>
        </ModalBody>
        <ModalFooter gap='2'>
          <RMButton variant='outline' onClick={closeModal}>Cancel</RMButton>
          <RMButton colorScheme='blue' isLoading={isSaving} onClick={handleSave}>{isEdit ? 'Save' : 'Add'}</RMButton>
        </ModalFooter>
      </ModalContent>
    </Modal>
  )
}

function SecretKvEntries({ clusterName, user }) {
  const { theme } = useTheme()
  const badgeVariant = theme === 'dark' ? 'solid' : 'subtle'
  const mutedColor = theme === 'dark' ? 'gray.400' : 'gray.500'
  const [list, setList] = useState({ entries: [], states: {}, plugins: [], pinned: false, dropsPending: false, dropped: 0 })
  const [error, setError] = useState('')
  const [editing, setEditing] = useState(null) // the entry edited, {} to add one
  const [removeName, setRemoveName] = useState('')
  const g = user?.grants || {}
  const canManage = g['cluster-settings'] !== false
  const canSwitch = canManage || !!g['db-config-flag']

  const fetchList = useCallback(async () => {
    if (!clusterName) return
    try {
      const { data, status } = await clusterService.getSecretKvList(clusterName)
      if (status >= 200 && status < 300 && data) {
        setList({ entries: data.entries || [], states: data.states || {}, plugins: data.plugins || [], pinned: !!data.pinned, dropsPending: !!data.dropsPending, dropped: data.dropped || 0 })
      } else {
        setError(typeof data === 'string' ? data : `Failed to list the vaults (status ${status})`)
      }
    } catch (e) {
      setError(e?.message || 'Failed to list the vaults')
    }
  }, [clusterName])

  useEffect(() => { fetchList() }, [fetchList])

  // returns the error, '' on success
  const call = async (fn) => {
    setError('')
    try {
      const { data, status } = await fn()
      if (status < 200 || status >= 300) {
        const msg = typeof data === 'string' ? data.trim() : `status ${status}`
        setError(msg)
        return msg
      }
      await fetchList()
      return ''
    } catch (e) {
      const msg = e?.response?.data || e?.message || 'Request failed'
      setError(String(msg))
      return String(msg)
    }
  }

  const manifest = list.plugins.find((p) => p.name === 'secret_kv-vault')
  const save = async (spec, isEdit) => {
    const err = await call(() => clusterService.putSecretKv(clusterName, spec, isEdit))
    if (!err) setEditing(null)
    return err
  }

  return (
    <Stack spacing='2' width='100%'>
      {list.entries.length === 0 && <Text as='span' color={mutedColor}>no vault: the versions live in the local file only</Text>}
      {list.entries.map((e) => {
        const state = list.states[e.name]
        // an entry from a Cloud18 marketplace offer: the back-office manages its settings, only
        // its switch is the cluster's
        const offer = e.offer
        return (
          <Flex key={e.name} align='center' gap='3' wrap='wrap'>
            <Switch isChecked={!!e.enabled} isDisabled={!canSwitch || list.pinned}
              onChange={() => call(() => clusterService.switchSecretKv(clusterName, e.name, !e.enabled))} />
            <Text as='span' fontWeight='semibold'>{e.name}</Text>
            {statePill(e, state, badgeVariant)}
            {offer && (
              <Tooltip label='from the Cloud18 marketplace: its settings are managed by Cloud18, only its switch is yours' hasArrow>
                <Badge colorScheme='teal' variant={badgeVariant}>Cloud18{offer.title ? ` · ${offer.title}` : ''}{offer.partner ? ` · ${offer.partner}` : ''}</Badge>
              </Tooltip>
            )}
            <Badge colorScheme='purple' variant={badgeVariant}>{e.settings?.address || 'no address'}</Badge>
            <Text as='span' fontSize='sm' color={mutedColor}>
              {(e.settings?.mount || 'kv') + '/' + (e.settings?.['path-prefix'] ? e.settings['path-prefix'] + '/' : '') + clusterName}
              {e.settings?.mode === 'database_engine' ? ' (database engine)' : ''}
              {state?.lastWrite && !state.lastWrite.startsWith('0001') ? ` · last write ${new Date(state.lastWrite).toLocaleString()}` : ''}
            </Text>
            {!offer && (
              <>
                <RMButton size='sm' variant='outline' isDisabled={!canManage || list.pinned} onClick={() => setEditing(e)}>Edit</RMButton>
                <RMButton size='sm' variant='outline' isDisabled={!canManage || list.pinned} onClick={() => setRemoveName(e.name)}>Remove</RMButton>
              </>
            )}
          </Flex>
        )
      })}
      {list.dropsPending && (
        <Badge colorScheme='orange' variant={badgeVariant} alignSelf='flex-start'>{list.dropped} version(s) dropped: a full pass writes them again</Badge>
      )}
      <Flex gap='2' align='center'>
        <RMButton size='sm' isDisabled={!canManage || list.pinned || !manifest} onClick={() => setEditing({})}>Add a vault</RMButton>
        {list.pinned && <Text as='span' fontSize='sm' color={mutedColor}>set by a config file (secret-kv): change them there</Text>}
      </Flex>
      {error && <Text color={theme === 'dark' ? 'red.300' : 'red.500'} fontSize='sm'>{error}</Text>}
      {editing && (
        <SecretKvFormModal isOpen={!!editing} closeModal={() => setEditing(null)} manifest={manifest}
          entry={editing.name ? editing : null} onSave={save} />
      )}
      {removeName && (
        <ConfirmModal isOpen={!!removeName} closeModal={() => setRemoveName('')}
          title={`Remove ${removeName}? What the vault holds stays.`}
          onConfirmClick={() => { call(() => clusterService.removeSecretKv(clusterName, removeName)); setRemoveName('') }} />
      )}
    </Stack>
  )
}

export default SecretKvEntries
