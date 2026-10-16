import {
  Badge,
  Box,
  Flex,
  FormControl,
  FormHelperText,
  FormLabel,
  Input,
  Modal,
  ModalBody,
  ModalCloseButton,
  ModalContent,
  ModalFooter,
  ModalHeader,
  ModalOverlay,
  Select,
  Stack,
  Switch,
  Text
} from '@chakra-ui/react'
import React, { useCallback, useEffect, useState } from 'react'
import { useDispatch } from 'react-redux'
import RMButton from '../../components/RMButton'
import RMSwitch from '../../components/RMSwitch'
import Dropdown from '../../components/Dropdown'
import TextForm from '../../components/TextForm'
import ConfirmModal from '../../components/Modals/ConfirmModal'
import TextInputModal from '../../components/Modals/TextInputModal'
import { resticCopyRepo } from '../../redux/clusterSlice'
import parentStyles from '../../components/Modals/styles.module.scss'
import { clusterService } from '../../services/clusterService'
import { useTheme } from '../../ThemeProvider'
import { formatBytes } from '../../utility/common'

// The cluster's archives in the Archive settings (#2091), as settings rows: a first row picks
// the attached archive (None detaches; the one left is detached and stays with its content,
// still invoiced) and adds one; then each archive lists its own settings, drawn from its
// plugin's typed config, each saved on its own. Remove deletes the content then the entry.
// Secrets are only ever sent: the list gives the names of the secrets set. Each archive shows its size
// (an archive billed on its content is invoiced on it) and the number of backups of the
// catalogue it holds; remove's confirmation reads the size first and shows what is deleted.

const NAME_PATTERN = '^[a-z0-9][a-z0-9_-]{0,63}$'
// the settings every restic plugin shares start here in a manifest: shown under "More"
const FIRST_COMMON_SETTING = 'repo-append-cluster'

// plain labels of the plugins and their settings; a setting not named here shows its key
export const KIND_LABELS = {
  'archive-restic-local': 'Local folder',
  'archive-restic-sftp': 'SFTP server',
  'archive-restic-s3': 'S3 bucket'
}

const SETTING_LABELS = {
  'local-repository': 'Folder',
  'repository': 'Repository URL',
  'aws-endpoint': 'S3 endpoint',
  'aws-bucket': 'Bucket',
  'aws-prefix': 'Folder in the bucket',
  'aws-region': 'Region',
  'aws-access-key-id': 'Access key',
  'aws-access-secret': 'Secret key',
  's3-mode': 'S3 path layout',
  'password': 'Archive password',
  'dir-mode': 'Folder permissions',
  'file-mode': 'File permissions',
  'purge-oldest-on-disk-space': 'Purge oldest on low disk',
  'purge-oldest-on-disk-threshold': 'Low disk threshold (%)',
  'repo-append-cluster': 'Add cluster name to path',
  'additional-env': 'Extra environment',
  'tags': 'Tags',
  'host': 'Host name',
  'timeout': 'Timeout (s)',
  'dump-timeout': 'Dump timeout (s)',
  'auto-init': 'Create if missing'
}

const SETTING_HELP = {
  'local-repository': 'Where the archive is. Local folder: a path on the replication-manager host. SFTP server: sftp:user@host:/path.',
  'aws-prefix': 'A folder inside the bucket, so several archives can share a bucket. Never leave it empty for a shared bucket.',
  'password': 'Encrypts the archive. Needed to open it: keep it safe, a lost password loses the archive.',
  'repo-append-cluster': 'Adds the cluster name at the end of the path.',
  's3-mode': 'auto, new or legacy: how the bucket path is built. Leave on auto.',
  'purge-oldest-on-disk-space': 'When the disk is nearly full, the oldest snapshots are deleted first.'
}

export function settingLabel(name, plugin) {
  if (name === 'local-repository' && plugin === 'archive-restic-sftp') return 'Server and path'
  if (SETTING_LABELS[name]) return SETTING_LABELS[name]
  const words = name.replace(/^purge-/, 'retention ').replace(/^mount-/, 'mount ').replace(/-/g, ' ')
  return words.charAt(0).toUpperCase() + words.slice(1)
}

function summary(archive) {
  const s = archive.settings || {}
  if (s['aws-bucket']) return `${s['aws-endpoint'] || ''} ${s['aws-bucket']}${s['aws-prefix'] ? '/' + s['aws-prefix'] : ''}`.trim()
  if (s['repository']) return s['repository']
  if (s['local-repository']) return s['local-repository']
  return 'default repository'
}

function sizeText(archive) {
  if (!archive?.size) return 'not read'
  return formatBytes(archive.size.bytes)
}

function sizeTitle(archive) {
  if (!archive?.size?.at) return ''
  return `read ${new Date(archive.size.at).toLocaleString()}${archive.attached ? '' : ' (detached: unchanged since)'}`
}

const can = (list, archive, capability) =>
  (list.plugins.find((p) => p.name === archive.plugin)?.capabilities || []).includes(capability)

function splitSettings(manifest) {
  const settings = manifest?.settings || []
  const idx = settings.findIndex((st) => st.name === FIRST_COMMON_SETTING)
  const main = []
  const more = []
  settings.forEach((st, i) => {
    if (st.type === 'secret' || idx < 0 || i < idx) main.push(st)
    else more.push(st)
  })
  return { main, more }
}

function SettingField({ st, value, onChange, secretSet }) {
  if (st.type === 'bool') {
    return <Switch isChecked={value === true || value === 'true'} onChange={(e) => onChange(e.target.checked)} />
  }
  if (st.type === 'enum') {
    return (
      <Select size='sm' value={value ?? ''} onChange={(e) => onChange(e.target.value)} placeholder='default'>
        {(st.values || []).map((v) => <option key={v} value={v}>{v}</option>)}
      </Select>
    )
  }
  if (st.type === 'secret') {
    return (
      <Input size='sm' type='password' autoComplete='new-password' value={value ?? ''}
        placeholder={secretSet ? 'set: leave empty to keep it' : ''}
        onChange={(e) => onChange(e.target.value)} />
    )
  }
  return (
    <Input size='sm' type={st.type === 'int' ? 'number' : 'text'} value={value ?? ''}
      onChange={(e) => onChange(e.target.value)} />
  )
}

export function ArchiveFormModal({ isOpen, closeModal, plugins, archive, onSave }) {
  const { theme } = useTheme()
  const isEdit = !!archive
  const [plugin, setPlugin] = useState(archive?.plugin || plugins?.[0]?.name || '')
  const [name, setName] = useState(archive?.name || '')
  const [values, setValues] = useState({ ...(archive?.settings || {}) })
  const [showMore, setShowMore] = useState(false)
  const [error, setError] = useState('')
  const [isSaving, setIsSaving] = useState(false)
  const manifest = plugins.find((p) => p.name === plugin)
  const { main, more } = splitSettings(manifest)

  const setValue = (k, v) => setValues((prev) => ({ ...prev, [k]: v }))

  const handleSave = async () => {
    setError('')
    if (!isEdit && name && !new RegExp(NAME_PATTERN).test(name)) {
      setError('Name: lowercase letters, digits, - and _ only')
      return
    }
    const declared = new Set((manifest?.settings || []).map((st) => st.name))
    const settings = {}
    const unset = []
    Object.entries(values).forEach(([k, v]) => {
      if (!declared.has(k)) return
      if (v === '' || v === null || v === undefined) {
        if (isEdit && archive.settings && k in archive.settings) unset.push(k)
        return
      }
      settings[k] = String(v)
    })
    setIsSaving(true)
    const err = await onSave({ name: isEdit ? archive.name : name, plugin, settings, unset })
    setIsSaving(false)
    if (err) setError(err)
  }

  const renderField = (st) => (
    <FormControl key={st.name}>
      <FormLabel fontSize='sm' mb='1'>{settingLabel(st.name, plugin)}</FormLabel>
      <SettingField st={st} value={values[st.name]} onChange={(v) => setValue(st.name, v)}
        secretSet={(archive?.secretsSet || []).includes(st.name)} />
    </FormControl>
  )

  return (
    <Modal isOpen={isOpen} onClose={closeModal} size='lg'>
      <ModalOverlay />
      <ModalContent className={theme === 'light' ? parentStyles.modalLightContent : parentStyles.modalDarkContent}>
        <ModalHeader>{isEdit ? `Edit ${archive.name}` : 'Add an archive'}</ModalHeader>
        <ModalCloseButton />
        <ModalBody>
          <Stack spacing='3'>
            {!isEdit && (
              <>
                <FormControl>
                  <FormLabel fontSize='sm' mb='1'>Kind</FormLabel>
                  <Select size='sm' value={plugin} onChange={(e) => { setPlugin(e.target.value); setValues({}) }}>
                    {plugins.map((p) => <option key={p.name} value={p.name}>{KIND_LABELS[p.name] || p.name}</option>)}
                  </Select>
                </FormControl>
                <FormControl>
                  <FormLabel fontSize='sm' mb='1'>Name</FormLabel>
                  <Input size='sm' value={name} placeholder='archive-bso-s3-uk1 (empty: archive-<type>-<n>)'
                    onChange={(e) => setName(e.target.value.trim())} />
                </FormControl>
              </>
            )}
            {main.map(renderField)}
            {more.length > 0 && (
              <RMButton variant='link' onClick={() => setShowMore((v) => !v)}>
                {showMore ? 'Hide advanced settings' : `Advanced settings (${more.length})`}
              </RMButton>
            )}
            {showMore && more.map(renderField)}
            <FormHelperText>
              The repository is checked first: an existing one is opened (a wrong password is refused), a missing one is created.
            </FormHelperText>
            {error && <Text color='red.500' fontSize='sm'>{error}</Text>}
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


// the value as an editor shows it: a bool as a bool, the rest as text
const shown = (v) => (v === undefined || v === null ? '' : String(v))

// SecretRow: a secret is never shown, only whether it is set; a new value replaces it
function SecretRow({ isSet, isDisabled, onSave }) {
  const [value, setValue] = useState('')
  return (
    <Flex gap='2' align='center'>
      <Text fontSize='sm' minW='60px'>{isSet ? 'set' : 'not set'}</Text>
      <Input size='sm' maxW='260px' type='password' autoComplete='new-password' value={value}
        placeholder='new value' isDisabled={isDisabled} onChange={(e) => setValue(e.target.value)} />
      <RMButton size='xs' isDisabled={isDisabled || !value} onClick={async () => { if (!(await onSave(value))) setValue('') }}>Save</RMButton>
    </Flex>
  )
}

// useArchiveSettings is the archive part of the Archive settings: its rows, and the modals
// they open
export function useArchiveSettings({ clusterName, user, help }) {
  const dispatch = useDispatch()
  const { theme } = useTheme()
  const [list, setList] = useState({ attached: '', archives: [], plugins: [] })
  const [error, setError] = useState('')
  const [adding, setAdding] = useState(false)
  const [rename, setRename] = useState(null)
  const [removeName, setRemoveName] = useState('')
  const [open, setOpen] = useState({}) // archive name -> its settings shown
  const [advanced, setAdvanced] = useState({}) // archive name -> its advanced settings shown
  const [copySource, setCopySource] = useState('')
  const canSet = user?.grants?.['cluster-settings'] !== false
  const canRemove = user?.grants?.['db-backup'] !== false
  const badgeVariant = theme === 'dark' ? 'solid' : 'subtle'

  const fetchList = useCallback(async () => {
    if (!clusterName) return
    try {
      const { data, status } = await clusterService.getArchiveList(clusterName)
      if (status >= 200 && status < 300 && data) {
        setList({ attached: data.attached || '', archives: data.archives || [], plugins: data.plugins || [] })
      } else {
        setError(typeof data === 'string' ? data : `Failed to list the archives (status ${status})`)
      }
    } catch (e) {
      setError(e?.message || 'Failed to list the archives')
    }
  }, [clusterName])

  useEffect(() => { fetchList() }, [fetchList])

  // a remove or a size read runs in the background: follow it until it ends
  const running = list.archives.some((a) => a.status === 'removing' || a.status === 'sizing')
  useEffect(() => {
    if (!running) return undefined
    const t = setInterval(fetchList, 3000)
    return () => clearInterval(t)
  }, [running, fetchList])

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
      setError(e?.message || 'Request failed')
      return e?.message || 'Request failed'
    }
  }

  const saveSetting = (a, key, value) => call(() => clusterService.editArchive(clusterName, a.name,
    value === '' || value === null || value === undefined
      ? { settings: {}, unset: [key] }
      : { settings: { [key]: String(value) }, unset: [] }))

  const readSize = (a) => call(() => clusterService.sizeArchive(clusterName, a.name))
  const askRemove = (a) => {
    setRemoveName(a.name)
    if (can(list, a, 'size')) readSize(a)
  }

  const settingEditor = (a, st) => {
    const value = (a.settings || {})[st.name]
    const disabled = !canSet || a.status === 'removing'
    const title = `${a.name}: ${settingLabel(st.name, a.plugin)}`
    if (st.type === 'secret') {
      return <SecretRow isSet={(a.secretsSet || []).includes(st.name)} isDisabled={disabled} onSave={(v) => saveSetting(a, st.name, v)} />
    }
    if (st.type === 'bool') {
      return <RMSwitch isChecked={value === true || value === 'true'} isDisabled={disabled} confirmTitle={`Change ${title}?`}
        onChange={(checked) => saveSetting(a, st.name, checked ? 'true' : 'false')} />
    }
    if (st.type === 'enum') {
      const options = [{ name: 'default', value: '__default__' }, ...(st.values || []).map((v) => ({ name: v, value: v }))]
      return <Dropdown options={options} selectedValue={shown(value) || '__default__'} isDisabled={disabled} confirmTitle={`Change ${title}?`}
        onChange={(v) => saveSetting(a, st.name, v === '__default__' || v === 'default' ? '' : v)} />
    }
    return <TextForm value={shown(value)} isDisabled={disabled} confirmTitle={`Change ${title} to `}
      onSave={(v) => saveSetting(a, st.name, v)} />
  }

  const settingRow = (a, st) => ({
    key: settingLabel(st.name, a.plugin),
    help: help(`**${settingLabel(st.name, a.plugin)}**\n\n${SETTING_HELP[st.name] || ''}\n\nType: ${st.type}${st.values ? ` (${st.values.join(', ')})` : ''}\n\nConfig: \`archive.${a.name}.${st.name}\``, settingLabel(st.name, a.plugin)),
    value: settingEditor(a, st)
  })

  const statusBadge = (a) => {
    if (a.status) return <Badge ml='2' colorScheme={a.status === 'removing' || a.status === 'sizing' ? 'orange' : 'red'} variant={badgeVariant}>{a.status}</Badge>
    return a.attached
      ? <Badge ml='2' colorScheme='green' variant={badgeVariant}>attached</Badge>
      : <Badge ml='2' colorScheme='gray' variant={badgeVariant}>detached</Badge>
  }

  const archiveRows = (a) => {
    const manifest = list.plugins.find((p) => p.name === a.plugin)
    const { main, more } = splitSettings(manifest)
    const isOpen = open[a.name] ?? a.attached
    const busy = a.status === 'removing'
    const others = list.archives.filter((o) => o.name !== a.name)
    const rows = [
      { key: 'Kind', value: <Text fontSize='sm'>{KIND_LABELS[a.plugin] || a.plugin}</Text> },
      { key: 'Location', value: <Text fontSize='sm'>{a.location || summary(a)}</Text> },
      {
        key: 'Size',
        value: (
          <Flex gap='2' align='center'>
            <Text fontSize='sm' title={sizeTitle(a)}>{a.status === 'sizing' ? 'reading...' : sizeText(a)}, {a.backups || 0} backup(s)</Text>
            {can(list, a, 'size') && <RMButton size='xs' variant='outline' isDisabled={busy || a.status === 'sizing'} onClick={() => readSize(a)}>Read</RMButton>}
          </Flex>
        )
      }
    ]
    if (isOpen) {
      rows.push(...main.map((st) => settingRow(a, st)))
      if (more.length > 0) {
        rows.push({
          key: 'Advanced settings',
          value: <RMButton size='xs' variant='outline' onClick={() => setAdvanced((p) => ({ ...p, [a.name]: !p[a.name] }))}>{advanced[a.name] ? 'Hide' : `Show (${more.length})`}</RMButton>
        })
        if (advanced[a.name]) rows.push(...more.map((st) => settingRow(a, st)))
      }
      if (a.attached && others.length > 0) {
        rows.push({
          key: 'Copy backups from',
          help: help('**Copy backups from another archive**\n\nCopies the snapshots of another archive of the list into this one (restic copy), queued as a restic task.', 'Copy backups from'),
          value: (
            <Flex gap='2' align='center'>
              <Select size='sm' maxW='260px' value={copySource} placeholder='an archive' onChange={(e) => setCopySource(e.target.value)}>
                {others.map((o) => <option key={o.name} value={o.name}>{o.name}</option>)}
              </Select>
              <RMButton size='xs' isDisabled={!canSet || !copySource} onClick={() => dispatch(resticCopyRepo(clusterName, { source: { archive: copySource } }))}>Copy</RMButton>
            </Flex>
          )
        })
      }
      rows.push({
        key: 'Name',
        value: (
          <Flex gap='2'>
            <RMButton size='xs' variant='outline' isDisabled={!canSet || busy} onClick={() => setRename(a)}>Rename</RMButton>
            <RMButton size='xs' colorScheme='red' variant='outline' isDisabled={!canRemove || busy} onClick={() => askRemove(a)}>Remove (deletes its content)</RMButton>
          </Flex>
        )
      })
    } else {
      rows.push({
        key: 'Settings',
        value: <RMButton size='xs' variant='outline' onClick={() => setOpen((p) => ({ ...p, [a.name]: true }))}>Show settings</RMButton>
      })
    }
    return {
      key: <Flex align='center' wrap='wrap'><Text as='span'>{a.name}</Text>{statusBadge(a)}</Flex>,
      value: rows
    }
  }

  const attachOptions = [{ name: 'None', value: '__none__' }, ...list.archives.map((a) => ({ name: a.name, value: a.name }))]
  const rows = [
    {
      key: 'Attached archive',
      help: help('**Attached archive**\n\nThe archive the backups go to. Choosing another one detaches the current one: it stays below with its content, still invoiced. None: the backups stay in the servers\' backup folders.\n\n**Add** an archive from its location and password: an existing archive there is opened (a wrong password is refused), a missing one is created.\n\nAn archive can also be pushed in the cluster\'s `inject.toml` as an `[[archive]]` block.\n\nConfig: `backup-archive-name`', 'Attached archive'),
      value: (
        <Stack spacing='1' width='100%'>
          <Flex gap='2' align='center'>
            <Dropdown options={attachOptions} selectedValue={list.attached || '__none__'} isDisabled={!canSet}
              confirmTitle='Change the attached archive?'
              confirmBody={list.attached ? `The backups go to the archive chosen. ${list.attached} is detached: it keeps its content, still invoiced. Attach` : 'The backups go to'}
              onChange={(v) => call(() => (v === '__none__' || v === 'None' ? clusterService.detachArchive(clusterName) : clusterService.attachArchive(clusterName, v)))} />
            <RMButton size='sm' isDisabled={!canSet || list.plugins.length === 0} onClick={() => setAdding(true)}>Add</RMButton>
          </Flex>
          {!list.attached && <Text fontSize='sm'>No archive attached: the backups stay in the servers' backup folders.</Text>}
          {error && <Text color='red.500' fontSize='sm'>{error}</Text>}
        </Stack>
      )
    },
    // the attached archive first, then the others
    ...[...list.archives].sort((x, y) => (y.attached ? 1 : 0) - (x.attached ? 1 : 0)).map(archiveRows)
  ]

  const removeTarget = list.archives.find((a) => a.name === removeName)
  const removeBody = (a) => {
    const size = a.status === 'sizing'
      ? 'Reading its size...'
      : a.size ? `${formatBytes(a.size.bytes)} will be deleted.` : 'Its size could not be read.'
    const backups = a.backups > 0 ? ` ${a.backups} backup(s) are held there: a backup whose only copy is there leaves the backup list.` : ''
    return `The content of ${a.name} (${a.location || summary(a)}) is deleted, then the archive leaves the list. ${size}${a.attached ? ' It is attached: the cluster is left with no archive.' : ''}${backups} This cannot be undone.`
  }

  const modals = (
    <>
      {adding && (
        <ArchiveFormModal isOpen={adding} closeModal={() => setAdding(false)} plugins={list.plugins}
          onSave={async (spec) => {
            const err = await call(() => clusterService.addArchive(clusterName, { name: spec.name, plugin: spec.plugin, settings: spec.settings }))
            if (!err) setAdding(false)
            return err
          }} />
      )}
      {rename && (
        <TextInputModal isOpen={!!rename} closeModal={() => setRename(null)} title={`Rename ${rename.name}`}
          fieldname='New name' defaultValue={rename.name} regexPattern={NAME_PATTERN}
          onSave={async (value) => {
            const err = await call(() => clusterService.renameArchive(clusterName, rename.name, value))
            if (!err) setRename(null)
          }} />
      )}
      {removeTarget && (
        <ConfirmModal isOpen={!!removeTarget} closeModal={() => setRemoveName('')} title={`Remove ${removeTarget.name}?`}
          body={removeBody(removeTarget)} confirmButtonText={removeTarget.status === 'sizing' ? 'Reading the size...' : 'Remove and delete the content'}
          confirmButtonProps={{ isDisabled: removeTarget.status === 'sizing', colorScheme: 'red' }}
          onConfirmClick={async () => {
            if (removeTarget.status === 'sizing') return
            const name = removeTarget.name
            setRemoveName('')
            await call(() => clusterService.removeArchive(clusterName, name))
          }} />
      )}
    </>
  )

  return { rows, modals }
}
