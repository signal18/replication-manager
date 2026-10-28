import {
  Badge,
  Box,
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
  Select,
  Stack,
  Switch,
  Text
} from '@chakra-ui/react'
import React, { useCallback, useEffect, useState } from 'react'
import { useDispatch, useSelector } from 'react-redux'
import RMButton from '../../components/RMButton'
import RMSwitch from '../../components/RMSwitch'
import Dropdown from '../../components/Dropdown'
import TextForm from '../../components/TextForm'
import ConfirmModal from '../../components/Modals/ConfirmModal'
import TextInputModal from '../../components/Modals/TextInputModal'
import { resticCopyRepo, selectClusterS3Providers } from '../../redux/clusterSlice'
import { setSetting, switchSetting } from '../../redux/settingsSlice'
import NumberInput from '../../components/NumberInput'
import parentStyles from '../../components/Modals/styles.module.scss'
import { clusterService } from '../../services/clusterService'
import { useTheme } from '../../ThemeProvider'
import { formatBytes } from '../../utility/common'
import PurgePoliciesEditor from './PurgePoliciesEditor'

// The cluster's archives in the Archive settings (#2091), as settings rows: a first row picks
// the attached archive (None detaches; the one left is detached and stays with its content,
// still invoiced) and adds one; then each archive lists its own settings, drawn from its
// plugin's typed config, each saved on its own. Remove deletes the content then the entry.
// Secrets are only ever sent: the list gives the names of the secrets set. Each archive shows its size
// (an archive billed on its content is invoiced on it) and the number of backups of the
// catalogue it holds; remove's confirmation reads the size first and shows what is deleted.

const NAME_PATTERN = '^[a-z0-9][a-z0-9_-]{0,63}$'
// a manifest without groups (an older external plugin): its settings from this one on are advanced
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
  'auto-init': 'Create if missing',
  'purge-group-by': 'Group snapshots by',
  'purge-keep-tag': 'Always keep tag',
  'purge-host': 'Purge only host',
  'purge-tag': 'Purge only tag',
  'purge-path': 'Purge only path',
  'purge-prune': 'Free the space (prune)',
  'purge-prune-compact': 'Prune: compact',
  'purge-prune-max-unused': 'Prune: max unused',
  'purge-prune-max-repack-size': 'Prune: max repack size',
  'purge-prune-repack-cacheable-only': 'Prune: repack cacheable only',
  'purge-prune-repack-small': 'Prune: repack small',
  'purge-prune-repack-uncompressed': 'Prune: repack uncompressed',
  'purge-policies': 'Purge policies'
}

const SETTING_HELP = {
  'local-repository': 'Where the archive is. Local folder: a path on the replication-manager host. SFTP server: sftp:user@host:/path.',
  'aws-prefix': 'A folder inside the bucket, so several archives can share a bucket. Never leave it empty for a shared bucket.',
  'password': 'Encrypts the archive. Needed to open it: keep it safe, a lost password loses the archive.',
  'repo-append-cluster': 'Adds the cluster name at the end of the path.',
  's3-mode': 'auto, new or legacy: how the bucket path is built. Leave on auto.',
  'purge-oldest-on-disk-space': 'When the disk is nearly full, the oldest snapshots are deleted first.',
  'purge-group-by': 'How snapshots are grouped before the keep rules apply: host, paths, tags, or a mix (host,tags).',
  'purge-keep-tag': 'Snapshots with this tag are never purged.',
  'purge-prune': 'After the purge, delete the data no snapshot uses any more, so the space is really freed.',
  'purge-policies': 'This archive\'s own purge policies, replacing the cluster\'s retention policy and tagged policies. Each rule is a tag and its keep values: the snapshots carrying the tag are kept by them, counted apart. A * rule replaces the default (the cluster\'s keep values). Empty: the cluster\'s policies apply.'
}

// kindLabel names an archive's plugin first (restic is one plugin family among others to
// come), then what it stores into: restic-local (Local folder)
// the attached archive's pick, shared by the Archive settings and the Maintenance tab: None
// detaches, the one left keeps its content
export const ATTACH_NONE = '__none__'
export function attachOptionsOf(archives) {
  return [{ name: 'None', value: ATTACH_NONE }, ...archives.map((a) => ({ name: a.name, value: a.name }))]
}
export function attachConfirmBody(attached) {
  return attached ? `The backups go to the archive chosen. ${attached} is detached: it keeps its content, still invoiced. Attach` : 'The backups go to'
}
export function attachArchiveRequest(clusterName, value) {
  return value === ATTACH_NONE || value === 'None' ? clusterService.detachArchive(clusterName) : clusterService.attachArchive(clusterName, value)
}

export function kindLabel(plugin, manifest) {
  const name = (plugin || '').replace(/^archive-/, '')
  const what = KIND_LABELS[plugin] || manifest?.description
  return what ? `${name} (${what})` : name
}

export function settingLabel(name, plugin) {
  if (name === 'local-repository' && plugin === 'archive-restic-sftp') return 'Server and path'
  if (SETTING_LABELS[name]) return SETTING_LABELS[name]
  const words = name.replace(/^purge-/, 'retention ').replace(/^mount-/, 'mount ').replace(/-/g, ' ')
  return words.charAt(0).toUpperCase() + words.slice(1)
}

export function summary(archive) {
  const s = archive.settings || {}
  if (s['aws-bucket']) return `${s['aws-endpoint'] || ''} ${s['aws-bucket']}${s['aws-prefix'] ? '/' + s['aws-prefix'] : ''}`.trim()
  if (s['repository']) return s['repository']
  if (s['local-repository']) return s['local-repository']
  return 'default repository'
}

export function sizeText(archive) {
  if (!archive?.size) return 'not read'
  return formatBytes(archive.size.bytes)
}

function sizeTitle(archive) {
  if (!archive?.size?.at) return ''
  return `read ${new Date(archive.size.at).toLocaleString()}${archive.attached ? '' : ' (detached: unchanged since)'}`
}

const can = (list, archive, capability) =>
  (list.plugins.find((p) => p.name === archive.plugin)?.capabilities || []).includes(capability)

// the backup catalogue can be rebuilt from an archive whose snapshots replication-manager can
// read: a plugin's that lists a snapshot's content, the attached restic archive
const canRebuild = (list, archive) =>
  can(list, archive, 'contents') || (archive.attached && (archive.plugin || '').startsWith('archive-restic'))

// rebuildText tells the last rebuild of the catalogue from the archive
function rebuildText(r) {
  if (!r) return 'never rebuilt from it'
  const at = new Date(r.at).toLocaleString()
  if (r.error) return `rebuild of ${at} failed: ${r.error}`
  const parts = [`${r.read} of ${r.snapshots} snapshot(s) read`]
  if (r.restored) parts.push(`${r.restored} backup(s) only in the archive added`)
  if (r.linked) parts.push(`${r.linked} backup(s) given a snapshot`)
  if (r.unreadable) parts.push(`${r.unreadable} not readable`)
  if ((r.missing || []).length) parts.push(`${r.missing.length} recorded snapshot(s) not in the archive`)
  return `rebuilt ${at}: ${parts.join(', ')}`
}

// splitSettings lays a plugin's settings out by their group: the main ones (location,
// credentials), how it purges, the advanced ones
function splitSettings(manifest) {
  const settings = manifest?.settings || []
  const grouped = settings.some((st) => st.group)
  const idx = settings.findIndex((st) => st.name === FIRST_COMMON_SETTING)
  const main = []
  const purge = []
  const more = []
  settings.forEach((st, i) => {
    if (st.type === 'secret') main.push(st)
    else if (grouped) (st.group === 'purge' ? purge : st.group ? more : main).push(st)
    else if (idx < 0 || i < idx) main.push(st)
    else more.push(st)
  })
  return { main, purge, more }
}

// the cluster's retention policy: the user's, applied by the attached archive
const KEEP_COUNTS = [
  ['backup-keep-last', 'backupKeepLast', 'Keep last', 'How many of the newest backups are kept.'],
  ['backup-keep-hourly', 'backupKeepHourly', 'Keep hourly', 'For the last N hours that have a backup, the newest of each hour is kept.'],
  ['backup-keep-daily', 'backupKeepDaily', 'Keep daily', 'For the last N days that have a backup, the newest of each day is kept.'],
  ['backup-keep-weekly', 'backupKeepWeekly', 'Keep weekly', 'For the last N weeks that have a backup, the newest of each week is kept.'],
  ['backup-keep-monthly', 'backupKeepMonthly', 'Keep monthly', 'For the last N months that have a backup, the newest of each month is kept.'],
  ['backup-keep-yearly', 'backupKeepYearly', 'Keep yearly', 'For the last N years that have a backup, the newest of each year is kept.']
]
const KEEP_WITHIN = [
  ['backup-keep-within', 'backupKeepWithin', 'Keep all within', 'Every backup newer than this is kept, counted from the newest one.'],
  ['backup-keep-within-hourly', 'backupKeepWithinHourly', 'Keep hourly within', 'The newest backup of each hour within this duration is kept.'],
  ['backup-keep-within-daily', 'backupKeepWithinDaily', 'Keep daily within', 'The newest backup of each day within this duration is kept.'],
  ['backup-keep-within-weekly', 'backupKeepWithinWeekly', 'Keep weekly within', 'The newest backup of each week within this duration is kept.'],
  ['backup-keep-within-monthly', 'backupKeepWithinMonthly', 'Keep monthly within', 'The newest backup of each month within this duration is kept.'],
  ['backup-keep-within-yearly', 'backupKeepWithinYearly', 'Keep yearly within', 'The newest backup of each year within this duration is kept.']
]
const DURATION_PATTERN = '^(\\d+y)?(\\d+m)?(\\d+d)?(\\d+h)?$'

function SettingField({ st, value, onChange, secretSet, s3Providers }) {
  // the archive's S3 storage: one of the cluster's S3 providers (an offer's is added first)
  if (st.name === 's3-provider' && s3Providers) {
    const names = [...new Set([...s3Providers.map((p) => p.name), ...(value ? [value] : [])])]
    return (
      <Select size='sm' value={value ?? ''} onChange={(e) => onChange(e.target.value)} placeholder='an S3 provider'>
        {names.map((n) => <option key={n} value={n}>{n}</option>)}
      </Select>
    )
  }
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

// preset: a new archive's plugin, name and settings chosen beforehand (an archive offer)
export function ArchiveFormModal({ isOpen, closeModal, plugins, archive, onSave, enterprisePlan, s3Providers, preset }) {
  const { theme } = useTheme()
  const isEdit = !!archive
  const [plugin, setPlugin] = useState(archive?.plugin || preset?.plugin || plugins?.find((p) => p.tier !== 'enterprise' || enterprisePlan)?.name || '')
  const [name, setName] = useState(archive?.name || preset?.name || '')
  const [values, setValues] = useState({ ...(archive?.settings || preset?.settings || {}) })
  const [showMore, setShowMore] = useState(false)
  const [error, setError] = useState('')
  const [isSaving, setIsSaving] = useState(false)
  const manifest = plugins.find((p) => p.name === plugin)
  const { main, purge, more: advanced } = splitSettings(manifest)
  const more = [...purge, ...advanced]

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
        secretSet={(archive?.secretsSet || []).includes(st.name)} s3Providers={s3Providers} />
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
                  <FormLabel fontSize='sm' mb='1'>Plugin</FormLabel>
                  <Select size='sm' value={plugin} onChange={(e) => { setPlugin(e.target.value); setValues({}) }}>
                    {plugins.map((p) => {
                      const locked = p.tier === 'enterprise' && !enterprisePlan
                      return (
                        <option key={p.name} value={p.name} disabled={locked}>
                          {kindLabel(p.name, p)}{p.tier === 'enterprise' ? (locked ? ' (Enterprise: needs a partner, support or developer plan)' : ' (Enterprise)') : ''}
                        </option>
                      )
                    })}
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
            <Text fontSize='sm' opacity='0.7'>
              The repository is checked first: an existing one is opened (a wrong password is refused), a missing one is created.
            </Text>
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
export function useArchiveSettings({ clusterName, user, help, config }) {
  const dispatch = useDispatch()
  const { theme } = useTheme()
  const [list, setList] = useState({ attached: '', archives: [], plugins: [], enterprisePlan: false, catalogueRebuilding: '', catalogueRebuilds: [] })
  const [error, setError] = useState('')
  const [adding, setAdding] = useState(false) // true, or the preset of an archive offer
  const [offers, setOffers] = useState([])
  const [requesting, setRequesting] = useState(false) // a Request is being saved
  const s3Providers = useSelector(selectClusterS3Providers)
  const [rename, setRename] = useState(null)
  const [removeName, setRemoveName] = useState('')
  const [open, setOpen] = useState({}) // archive name -> its settings shown
  const [advanced, setAdvanced] = useState({}) // archive name -> its advanced settings shown
  const [purgeShown, setPurgeShown] = useState({}) // archive name -> its purge options shown
  const [copySource, setCopySource] = useState('')
  const canSet = user?.grants?.['cluster-settings'] !== false
  const canRemove = user?.grants?.['db-backup'] !== false
  const badgeVariant = theme === 'dark' ? 'solid' : 'subtle'

  const fetchList = useCallback(async () => {
    if (!clusterName) return
    try {
      const { data, status } = await clusterService.getArchiveList(clusterName)
      if (status >= 200 && status < 300 && data) {
        setList({
          attached: data.attached || '', archives: data.archives || [], plugins: data.plugins || [], enterprisePlan: !!data.enterprisePlan,
          catalogueRebuilding: data.catalogueRebuilding || '', catalogueRebuilds: data.catalogueRebuilds || []
        })
      } else {
        setError(typeof data === 'string' ? data : `Failed to list the archives (status ${status})`)
      }
    } catch (e) {
      setError(e?.message || 'Failed to list the archives')
    }
  }, [clusterName])

  useEffect(() => { fetchList() }, [fetchList])

  // the marketplace's archive offers (the catalogue the back-office pushes to every instance)
  const fetchOffers = useCallback(async () => {
    if (!clusterName) return
    try {
      const { data, status } = await clusterService.getArchiveOffers(clusterName)
      setOffers(status >= 200 && status < 300 && Array.isArray(data) ? data : [])
    } catch (e) {
      setOffers([])
    }
  }, [clusterName])

  useEffect(() => { fetchOffers() }, [fetchOffers])

  // an offer reached with the Cloud18 SSO: its S3 provider is added to the cluster (no key),
  // then the archive form opens with its plugin and that provider; repman gives the folder
  const takeOffer = async (o) => {
    if (!o.s3) return
    if (!o.added) {
      const err = await call(() => clusterService.addS3Provider(clusterName, {
        name: o.s3.name, providerSource: 'custom', endpoint: o.s3.endpoint, region: o.s3.region, cloud18Sso: true,
        ...(o.s3.bucket ? { bucket: o.s3.bucket } : {})
      }))
      if (err) return
      fetchOffers()
    }
    const taken = list.archives.some((a) => a.name === o.name)
    setAdding({ plugin: o.plugin, name: taken ? '' : o.name, settings: { 's3-provider': o.s3.name } })
  }

  // an offer whose keys Cloud18 issues: asked in cloud18-archive-offer-requests; the back-office
  // creates the cluster's bucket and keys and delivers its S3 provider with the cluster's config
  // the requests already saved are read from the offers (requested), refreshed after each one,
  // and one Request runs at a time: two clicks never overwrite each other
  const requestOffer = async (o) => {
    if (requesting) return
    const asked = new Set([
      ...(config?.cloud18ArchiveOfferRequests || '').split(',').map((n) => n.trim()).filter(Boolean),
      ...offers.filter((x) => x.requested).map((x) => x.name)
    ])
    if (asked.has(o.name)) return
    setRequesting(true)
    try {
      await dispatch(setSetting({ clusterName, setting: 'cloud18-archive-offer-requests', value: [...asked, o.name].join(',') }))
      await fetchOffers()
    } finally {
      setRequesting(false)
    }
  }

  // a remove, a size read or a catalogue rebuild runs in the background: follow it until it ends
  const running = list.archives.some((a) => a.status === 'removing' || a.status === 'sizing') || !!list.catalogueRebuilding
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
  const rebuildCatalogue = (a) => call(() => clusterService.rebuildBackupCatalogue(clusterName, a.name))
  const [dropMissing, setDropMissing] = useState(null) // the archive whose missing snapshots are to drop
  const askRemove = (a) => {
    setRemoveName(a.name)
    if (can(list, a, 'size')) readSize(a)
  }

  const settingEditor = (a, st) => {
    const value = (a.settings || {})[st.name]
    const disabled = !canSet || a.status === 'removing'
    const title = `${a.name}: ${settingLabel(st.name, a.plugin)}`
    if (st.name === 'purge-policies') {
      return (
        <Stack spacing='1' width='100%'>
          {!shown(value) && <Text fontSize='sm' opacity='0.7'>The cluster's retention policy and tagged policies apply.</Text>}
          <PurgePoliciesEditor value={shown(value)} allowDefault isDisabled={disabled} onSave={(v) => saveSetting(a, st.name, v)} />
        </Stack>
      )
    }
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
    const { main, purge, more } = splitSettings(manifest)
    const isOpen = open[a.name] ?? a.attached
    const busy = a.status === 'removing'
    const others = list.archives.filter((o) => o.name !== a.name)
    const rows = [
      { key: 'Plugin', value: <Text fontSize='sm'>{kindLabel(a.plugin, manifest)}</Text> },
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
    if (canRebuild(list, a)) {
      const last = list.catalogueRebuilds.find((r) => r.archive === a.name)
      const rebuilding = list.catalogueRebuilding.split(', ').includes(a.name)
      rows.push({
        key: 'Backup catalogue',
        help: help('**Backup catalogue**\n\nThe backups replication-manager knows, and the snapshots holding them. **Rebuild** reads the backup records in each snapshot of this archive the catalogue does not know: a backup only in the archive (a newer backup of its type replaced it) joins the list again. It also runs at the first start with no catalogue and when a standby becomes active. A snapshot the catalogue records that the archive no longer lists raises a warning, never fixed silently.', 'Backup catalogue'),
        value: (
          <Flex gap='2' align='center' wrap='wrap'>
            <Text fontSize='sm' color={last?.error || (last?.missing || []).length || last?.unreadable ? 'orange.400' : undefined}>{rebuilding ? 'rebuilding...' : rebuildText(last)}</Text>
            <RMButton size='xs' variant='outline' isDisabled={!canRemove || busy || !!list.catalogueRebuilding} onClick={() => rebuildCatalogue(a)}>Rebuild</RMButton>
            {(last?.missing || []).length > 0 && !rebuilding && (
              <RMButton size='xs' colorScheme='orange' variant='outline' isDisabled={!canRemove || busy || !!list.catalogueRebuilding}
                onClick={() => setDropMissing({ archive: a.name, missing: last.missing })}>Drop missing ({last.missing.length})</RMButton>
            )}
          </Flex>
        )
      })
    }
    if (isOpen) {
      rows.push(...main.map((st) => settingRow(a, st)))
      if (purge.length > 0 && can(list, a, 'purge')) {
        rows.push({
          key: 'Purge options',
          help: help('**Purge options**\n\nHow this archive applies the retention policy: which snapshots are compared, which are always kept, and whether the space is freed. What to keep is the cluster\'s policy, above, unless this archive sets its own purge policies.', 'Purge options'),
          value: <RMButton size='xs' variant='outline' onClick={() => setPurgeShown((p) => ({ ...p, [a.name]: !p[a.name] }))}>{purgeShown[a.name] ? 'Hide' : `Show (${purge.length})`}</RMButton>
        })
        if (purgeShown[a.name]) rows.push(...purge.map((st) => settingRow(a, st)))
      }
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

  const setClusterSetting = (setting, value) => dispatch(setSetting({ clusterName, setting, value }))
  const onSchedule = config?.backupArchivePurgeTrigger === 'schedule'
  const policyRows = [
    ...KEEP_COUNTS.map(([setting, field, label, text]) => ({
      key: label,
      help: help(`**${label}**\n\n${text} 0: not part of the policy.\n\nConfig: \`${setting}\``, label),
      value: <NumberInput value={config?.[field]} min={0} isDisabled={!canSet} showEditButton={true} showConfirmModal={true}
        confirmTitle={`Confirm update '${setting}':`} onConfirm={(v) => setClusterSetting(setting, v)} />
    })),
    ...KEEP_WITHIN.map(([setting, field, label, text]) => ({
      key: label,
      help: help(`**${label}**\n\n${text} Example 2y5m7d3h. Empty: not part of the policy.\n\nConfig: \`${setting}\``, label),
      value: <TextForm value={config?.[field] || ''} isDisabled={!canSet} regexPattern={DURATION_PATTERN}
        confirmTitle={`Confirm update '${setting}' to `} onSave={(v) => setClusterSetting(setting, v)} />
    })),
    {
      key: 'Tagged policies',
      help: help('**Tagged policies**\n\nThe snapshots carrying a tag kept by their own keep values, counted apart from the keep values above (the default policy), so they never push a backup out of keep last. The tags replication-manager sets: `trigger:backup` (a backup), `trigger:manual` (the Archive action), `trigger:snapback`, `trigger:binlogs` (a binlog-only snapshot). The binlog-only snapshots taken since the last backup are always kept: a binlog only extends a backup. An archive with its own purge policies applies those instead.\n\nConfig: `backup-keep-tag-policies`', 'Tagged policies'),
      value: <PurgePoliciesEditor value={config?.backupKeepTagPolicies || ''} isDisabled={!canSet}
        onSave={(v) => setClusterSetting('backup-keep-tag-policies', v)} />
    },
    {
      key: 'Remove original once archived',
      help: help('**Remove original once archived**\n\nOnce the attached archive holds a backup and it is checked, the backup is deleted from the server\'s backup folder. A restore fetches it back from the archive first.\n\nConfig: `backup-archive-remove-original`', 'Remove original once archived'),
      value: <RMSwitch isChecked={!!config?.backupArchiveRemoveOriginal} isDisabled={!canSet}
        confirmTitle='Confirm switch settings for backup-archive-remove-original?'
        onChange={() => dispatch(switchSetting({ clusterName, setting: 'backup-archive-remove-original' }))} />
    },
    {
      key: 'Purge',
      help: help('**Purge**\n\nWhen the attached archive is asked to apply the policy. After each backup: every backup it receives is followed by a purge. On schedule: at the cron below (needs the cluster scheduler, `monitoring-scheduler`).\n\n**Purge now** asks it at once.\n\nConfig: `backup-archive-purge-trigger`', 'Purge'),
      value: (
        <Flex gap='2' align='center' wrap='wrap'>
          <Dropdown options={[{ name: 'After each backup', value: 'after-backup' }, { name: 'On schedule', value: 'schedule' }]}
            selectedValue={onSchedule ? 'schedule' : 'after-backup'} isDisabled={!canSet} confirmTitle='Change when the archive purges?'
            onChange={(v) => setClusterSetting('backup-archive-purge-trigger', v)} />
          <RMButton size='xs' variant='outline' isDisabled={!canRemove || !list.attached}
            onClick={() => call(() => clusterService.purgeResticByPolicy(clusterName))}>Purge now</RMButton>
        </Flex>
      )
    },
    ...(onSchedule ? [{
      key: 'Purge schedule',
      help: help('**Purge schedule**\n\nCron with 6 fields (seconds first). Default: every hour.\n\nConfig: `scheduler-archive-purge-cron`', 'Purge schedule'),
      value: (
        <Stack spacing='1'>
          <TextForm value={config?.schedulerArchivePurgeCron || ''} isDisabled={!canSet}
            confirmTitle="Confirm update 'scheduler-archive-purge-cron' to " onSave={(v) => setClusterSetting('scheduler-archive-purge-cron', v)} />
          {!config?.monitoringScheduler && <Text fontSize='sm' color='orange.400'>The cluster scheduler is off: the schedule does not run.</Text>}
        </Stack>
      )
    }] : [])
  ]

  const attachOptions = attachOptionsOf(list.archives)
  const rows = [
    {
      key: 'Attached archive',
      help: help('**Attached archive**\n\nThe archive the backups go to. Choosing another one detaches the current one: it stays below with its content, still invoiced. None: the backups stay in the servers\' backup folders.\n\n**Add** an archive from its location and password: an existing archive there is opened (a wrong password is refused), a missing one is created.\n\nAn archive can also be pushed in the cluster\'s `inject.toml` as an `[[archive]]` block.\n\nConfig: `backup-archive-name`', 'Attached archive'),
      value: (
        <Stack spacing='1' width='100%'>
          <Flex gap='2' align='center'>
            <Dropdown options={attachOptions} selectedValue={list.attached || ATTACH_NONE} isDisabled={!canSet}
              confirmTitle='Change the attached archive?'
              confirmBody={attachConfirmBody(list.attached)}
              onChange={(v) => call(() => attachArchiveRequest(clusterName, v))} />
            <RMButton size='sm' isDisabled={!canSet || list.plugins.length === 0} onClick={() => setAdding(true)}>Add</RMButton>
          </Flex>
          {!list.attached && <Text fontSize='sm'>No archive attached: the backups stay in the servers' backup folders.</Text>}
          {error && <Text color='red.500' fontSize='sm'>{error}</Text>}
        </Stack>
      )
    },
    ...(offers.length > 0 ? [{
      key: 'Cloud18 offers',
      help: help('**Cloud18 offers**\n\nThe partners\' storages sold for archives, with their price. A storage reached with the Cloud18 SSO is used at once: **Use** adds it to the cluster\'s S3 providers (no key: replication-manager signs in with its Cloud18 identity, in a folder of its own) and opens the archive form. A storage whose keys Cloud18 issues is **Request**ed: Cloud18 creates the cluster\'s own bucket and keys and delivers the S3 provider with the cluster\'s config, then the offer can be used. The other offers are on request.\n\nConfig: `cloud18-archive-offer-requests`', 'Cloud18 offers'),
      value: (
        <Stack spacing='2' width='100%'>
          {offers.map((o) => (
            <Flex key={o.name} gap='2' align='center' wrap='wrap'>
              <Box flex='1' minW='200px'>
                <Text fontSize='sm' fontWeight='semibold'>{o.title || o.name}</Text>
                <Text fontSize='xs'>
                  {[o.partner, o.dcLocation || o.dc,
                    o.priceEurMonth != null ? `${o.priceEurMonth} €/${o.sizeUnit}/month`
                      : o.priceEurBauMonth != null ? `${o.priceEurBauMonth} €/BAU/month` : 'price on request'].filter(Boolean).join(' · ')}
                </Text>
              </Box>
              {o.usable && <RMButton size='sm' isDisabled={!canSet || list.plugins.length === 0} onClick={() => takeOffer(o)}>{o.added ? 'Add an archive' : 'Use'}</RMButton>}
              {o.requestable && <RMButton size='sm' isDisabled={!canSet || requesting} onClick={() => requestOffer(o)}>Request</RMButton>}
              {!o.usable && <Text fontSize='xs' maxW='260px'>{o.reason}</Text>}
            </Flex>
          ))}
        </Stack>
      )
    }] : []),
    {
      key: 'Retention policy',
      help: help('**Retention policy**\n\nWhat the cluster keeps: the snapshots any rule keeps stay, the others are purged. The keep values are the default policy, the tagged policies take the snapshots carrying their tag. They apply to every archive, unless an archive sets its own purge policies; the attached archive is asked to apply them, its own way (its Purge options).', 'Retention policy'),
      value: policyRows
    },
    // the attached archive first, then the others
    ...[...list.archives].sort((x, y) => (y.attached ? 1 : 0) - (x.attached ? 1 : 0)).map(archiveRows),
    // the program the restic plugins run: shown only with a restic archive
    ...(list.archives.some((a) => (a.plugin || '').startsWith('archive-restic')) ? [
    {
      key: 'Restic program',
      value: [
        {
          key: 'Program path',
          help: help('**Restic program path**\n\nThe restic program the restic archives run. A server-wide setting, set in the config file.\n\nConfig: `backup-restic-binary-path`', 'Restic program path'),
          value: <Text fontSize='sm'>{config?.backupResticBinaryPath || '/usr/bin/restic'}</Text>
        },
        {
          key: 'Mount folder',
          help: help('**Restic archive mount folder**\n\nWhere a restic archive is mounted to browse its snapshots or reseed from it. Empty: `<working-dir>/<cluster>/mount`; a relative path is resolved under it.\n\nConfig: `backup-restic-mount-dir`', 'Restic archive mount folder'),
          value: <Text fontSize='sm'>{config?.backupResticMountDir || '<working-dir>/<cluster>/mount'}</Text>
        },
        {
          key: 'Allow unsafe mount',
          help: help('**Allow unsafe mount**\n\nUse a restic mount found at the mount folder that replication-manager did not start (made by hand, by a helper with FUSE rights, or lost track of after a restart). Unsafe: it may be of another archive, and whoever made it can unmount it during a reseed.\n\nConfig: `backup-restic-allow-unsafe-mount`', 'Allow unsafe mount'),
          value: <RMSwitch isChecked={!!config?.backupResticAllowUnsafeMount} isDisabled={!canSet}
            confirmTitle='Confirm switch settings for backup-restic-allow-unsafe-mount?'
            onChange={() => dispatch(switchSetting({ clusterName, setting: 'backup-restic-allow-unsafe-mount' }))} />
        }
      ]
    }
    ] : [])
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
        <ArchiveFormModal isOpen={!!adding} closeModal={() => setAdding(false)} plugins={list.plugins} enterprisePlan={list.enterprisePlan}
          s3Providers={s3Providers} preset={adding === true ? null : adding}
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
      {dropMissing && (
        <ConfirmModal isOpen={!!dropMissing} closeModal={() => setDropMissing(null)} title={`Drop the snapshots gone from ${dropMissing.archive}?`}
          body={`The catalogue records ${dropMissing.missing.length} snapshot(s) the archive no longer has (${dropMissing.missing.map((id) => id.slice(0, 8)).join(', ')}). The archive is listed again first: each one still gone is taken out of the catalogue, the backups recording it lose it, and a backup whose only copy it was leaves the backup list. Nothing is deleted in the archive.`}
          confirmButtonText='Drop them'
          onConfirmClick={async () => {
            const name = dropMissing.archive
            setDropMissing(null)
            await call(() => clusterService.dropMissingCatalogueSnapshots(clusterName, name))
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
