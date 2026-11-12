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
import { globalClustersService } from '../../services/globalClustersService'
import { useTheme } from '../../ThemeProvider'
import { NAME_PATTERN, SettingField } from './ArchiveListSettings'

// The vault entries of a level (CREDENTIAL_STORES.md "Two levels"). A cluster's (#2227): each
// enabled entry gets every version of every secret of the cluster at
// <mount>/<path-prefix>/<cluster>/databases|proxies|apps|archives/<name>/<key>. The server's
// (level 'server', in the global settings): the server secrets at
// <mount>/<path-prefix>/system/repman/<instance name>/<key>, or in the OpenSVC secret
// system/sec/repman or a Kubernetes Secret repman-system (keys <instance name>_<key>) through a
// cluster's connection; the instance's sysops and extsysops only. Secrets go in through the
// form and never come back (secretsSet).

// the API of each level
const API = {
  cluster: (clusterName) => ({
    list: () => clusterService.getSecretKvList(clusterName),
    put: (spec, isEdit) => clusterService.putSecretKv(clusterName, spec, isEdit),
    switch: (name, enabled) => clusterService.switchSecretKv(clusterName, name, enabled),
    remove: (name) => clusterService.removeSecretKv(clusterName, name)
  }),
  server: () => ({
    list: () => globalClustersService.getServerSecretKvList(),
    put: (spec, isEdit) => globalClustersService.putServerSecretKv(spec, isEdit),
    switch: (name, enabled) => globalClustersService.switchServerSecretKv(name, enabled),
    remove: (name) => globalClustersService.removeServerSecretKv(name)
  })
}

const SETTING_LABELS = {
  address: 'Address',
  mount: 'Mount (KV v2)',
  'path-prefix': 'Path prefix',
  mode: 'Mode',
  auth: 'Sign-in',
  'role-id': 'Role id',
  'secret-id': 'Secret id',
  cluster: 'Through the cluster',
  namespace: 'Namespace'
}

// each plugin a level's list may hold: its add button and what it says
const PLUGINS = {
  'secret_kv-vault': {
    add: 'Add a vault',
    server: 'The server secrets (api-credentials, SMTP, GitLab...) are written under the mount, at path-prefix/system/repman/<instance name>. Only the sysops and extsysops of this instance see this list. repman never deletes what the vault holds.',
    cluster: 'The secrets are written under the mount, at path-prefix/cluster. repman never deletes what the vault holds.'
  },
  'secret_kv-opensvc': {
    add: 'Add OpenSVC',
    server: 'The server secrets are written to the OpenSVC secret system/sec/repman, keys <instance name>_<key>, through the agent of the cluster named here (a cluster on OpenSVC). The OpenSVC cluster\'s administrators can read them. repman never deletes a key.'
  },
  'secret_kv-kubernetes': {
    add: 'Add Kubernetes',
    server: 'The server secrets are written to the Secret repman-system of the namespace, keys <instance name>_<key>, through the client of the cluster named here (a cluster on Kubernetes). Whoever reads Secrets in that namespace can read them. repman never deletes a key.'
  }
}

// the state pill of an entry: off, behind (WARN0271) with its reason, or on
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

function SecretKvFormModal({ isOpen, closeModal, manifest, entry, onSave, level }) {
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
        <ModalHeader>{isEdit ? `Edit ${entry.name}` : (PLUGINS[manifest?.name]?.add || 'Add a store')}</ModalHeader>
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
              {PLUGINS[manifest?.name]?.[level === 'server' ? 'server' : 'cluster']}
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

function SecretKvEntries({ clusterName, user, level = 'cluster' }) {
  const isServer = level === 'server'
  const api = isServer ? API.server() : API.cluster(clusterName)
  const { theme } = useTheme()
  const badgeVariant = theme === 'dark' ? 'solid' : 'subtle'
  const mutedColor = theme === 'dark' ? 'gray.400' : 'gray.500'
  const [list, setList] = useState({ entries: [], states: {}, plugins: [], pinned: false, dropsPending: false, dropped: 0, instanceName: '' })
  // the server level answers 403 to anyone without the server credentials grant on every cluster
  const [forbidden, setForbidden] = useState(false)
  const [error, setError] = useState('')
  const [editing, setEditing] = useState(null) // the entry edited, {} to add one
  const [removeName, setRemoveName] = useState('')
  const g = user?.grants || {}
  // the server level: the list loads for its owners only, who may do everything
  const canManage = isServer || g['cluster-settings'] !== false
  const canSwitch = canManage || !!g['db-config-flag']

  const fetchList = useCallback(async () => {
    if (!isServer && !clusterName) return
    try {
      const { data, status } = await api.list()
      if (status === 403) {
        setForbidden(true)
        return
      }
      if (status >= 200 && status < 300 && data) {
        setList({ entries: data.entries || [], states: data.states || {}, plugins: data.plugins || [], pinned: !!data.pinned, dropsPending: !!data.dropsPending, dropped: data.dropped || 0, instanceName: data.instanceName || '' })
      } else {
        setError(typeof data === 'string' ? data : `Failed to list the vaults (status ${status})`)
      }
    } catch (e) {
      if (e?.response?.status === 403) {
        setForbidden(true)
        return
      }
      setError(e?.message || 'Failed to list the vaults')
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [clusterName, level])

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

  // the plugins this level's list takes, in the order of PLUGINS
  const manifests = Object.keys(PLUGINS).map((n) => list.plugins.find((p) => p.name === n)).filter(Boolean)
  const manifestOf = (plugin) => list.plugins.find((p) => p.name === (plugin || 'secret_kv-vault'))
  const save = async (spec, isEdit) => {
    const err = await call(() => api.put(spec, isEdit))
    if (!err) setEditing(null)
    return err
  }

  if (forbidden) {
    return <Text as='span' color={mutedColor}>the sysops and extsysops of this instance only</Text>
  }
  // where an entry writes: the server's folder is the instance's name, unknown while not complete
  const isVault = (e) => !e.plugin || e.plugin === 'secret_kv-vault'
  const folder = (e, state) => {
    const base = isVault(e) ? (e.settings?.mount || 'kv') + '/' : ''
    if (isServer) return state?.path ? base + state.path : base + '… (name this instance first)'
    return base + (e.settings?.['path-prefix'] ? e.settings['path-prefix'] + '/' : '') + clusterName
  }
  const target = (e) => {
    if (isVault(e)) return e.settings?.address || 'no address'
    return (e.plugin === 'secret_kv-opensvc' ? 'OpenSVC' : 'Kubernetes') + ' through ' + (e.settings?.cluster || '?')
  }

  return (
    <Stack spacing='2' width='100%'>
      {list.entries.length === 0 && <Text as='span' color={mutedColor}>no store: the versions live in the local file only</Text>}
      {list.entries.map((e) => {
        const state = list.states[e.name]
        // an entry from a Cloud18 marketplace offer: the back-office manages its settings, only
        // its switch is the cluster's
        const offer = e.offer
        return (
          <Flex key={e.name} align='center' gap='3' wrap='wrap'>
            <Switch isChecked={!!e.enabled} isDisabled={!canSwitch || list.pinned}
              onChange={() => call(() => api.switch(e.name, !e.enabled))} />
            <Text as='span' fontWeight='semibold'>{e.name}</Text>
            {statePill(e, state, badgeVariant)}
            {offer && (
              <Tooltip label='from the Cloud18 marketplace: its settings are managed by Cloud18, only its switch is yours' hasArrow>
                <Badge colorScheme='teal' variant={badgeVariant}>Cloud18{offer.title ? ` · ${offer.title}` : ''}{offer.partner ? ` · ${offer.partner}` : ''}</Badge>
              </Tooltip>
            )}
            <Badge colorScheme='purple' variant={badgeVariant}>{target(e)}</Badge>
            <Text as='span' fontSize='sm' color={mutedColor}>
              {folder(e, state)}
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
        {manifests.map((m) => (
          <RMButton key={m.name} size='sm' isDisabled={!canManage || list.pinned} onClick={() => setEditing({ plugin: m.name })}>{PLUGINS[m.name].add}</RMButton>
        ))}
        {list.pinned && <Text as='span' fontSize='sm' color={mutedColor}>set by a config file ({isServer ? 'server-secret-kv' : 'secret-kv'}): change them there</Text>}
      </Flex>
      {error && <Text color={theme === 'dark' ? 'red.300' : 'red.500'} fontSize='sm'>{error}</Text>}
      {editing && (
        <SecretKvFormModal isOpen={!!editing} closeModal={() => setEditing(null)} manifest={manifestOf(editing.plugin)}
          entry={editing.name ? editing : null} onSave={save} level={level} />
      )}
      {removeName && (
        <ConfirmModal isOpen={!!removeName} closeModal={() => setRemoveName('')}
          title={`Remove ${removeName}? What the store holds stays.`}
          onConfirmClick={() => { call(() => api.remove(removeName)); setRemoveName('') }} />
      )}
    </Stack>
  )
}

export default SecretKvEntries
