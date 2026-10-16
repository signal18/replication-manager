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
  Radio,
  Select,
  Stack,
  Switch,
  Table,
  Tbody,
  Td,
  Text,
  Th,
  Thead,
  Tr
} from '@chakra-ui/react'
import React, { useCallback, useEffect, useState } from 'react'
import RMButton from '../../components/RMButton'
import ConfirmModal from '../../components/Modals/ConfirmModal'
import TextInputModal from '../../components/Modals/TextInputModal'
import parentStyles from '../../components/Modals/styles.module.scss'
import { clusterService } from '../../services/clusterService'
import { useTheme } from '../../ThemeProvider'

// The cluster's archive list (#2091): selecting an archive attaches it (the one attached
// before is detached and stays listed with its content, still invoiced); remove deletes the
// content then the entry. The forms are drawn from each plugin's typed config, and secrets
// are only ever sent: the list gives the names of the secrets set.

const NAME_PATTERN = '^[a-z0-9][a-z0-9_-]{0,63}$'
// the settings every restic plugin shares start here in a manifest: shown under "More"
const FIRST_COMMON_SETTING = 'repo-append-cluster'

function summary(archive) {
  const s = archive.settings || {}
  if (s['aws-bucket']) return `${s['aws-endpoint'] || ''} ${s['aws-bucket']}${s['aws-prefix'] ? '/' + s['aws-prefix'] : ''}`.trim()
  if (s['repository']) return s['repository']
  if (s['local-repository']) return s['local-repository']
  return 'default repository'
}

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

function ArchiveFormModal({ isOpen, closeModal, plugins, archive, onSave }) {
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
      <FormLabel fontSize='sm' mb='1'>{st.name}</FormLabel>
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
                  <FormLabel fontSize='sm' mb='1'>Plugin</FormLabel>
                  <Select size='sm' value={plugin} onChange={(e) => { setPlugin(e.target.value); setValues({}) }}>
                    {plugins.map((p) => <option key={p.name} value={p.name}>{p.name}: {p.description}</option>)}
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
                {showMore ? 'Fewer settings' : `More settings (${more.length})`}
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

function ArchiveListSettings({ clusterName, user }) {
  const { theme } = useTheme()
  const [list, setList] = useState({ attached: '', archives: [], plugins: [] })
  const [error, setError] = useState('')
  const [form, setForm] = useState(null) // { archive } for edit, {} for add
  const [confirm, setConfirm] = useState(null) // { title, body, run }
  const [rename, setRename] = useState(null)
  const canSet = user?.grants?.['cluster-settings'] !== false
  const canRemove = user?.grants?.['db-backup'] !== false

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

  // a remove runs in the background: follow it until the archive is gone
  const removing = list.archives.some((a) => a.status === 'removing')
  useEffect(() => {
    if (!removing) return undefined
    const t = setInterval(fetchList, 5000)
    return () => clearInterval(t)
  }, [removing, fetchList])

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

  const saveArchive = async (spec) => {
    const isEdit = !!form?.archive
    const err = await call(() => isEdit
      ? clusterService.editArchive(clusterName, spec.name, { settings: spec.settings, unset: spec.unset })
      : clusterService.addArchive(clusterName, { name: spec.name, plugin: spec.plugin, settings: spec.settings }))
    if (!err) setForm(null)
    return err
  }

  const askAttach = (a) => setConfirm({
    title: `Attach ${a.name}?`,
    body: list.attached
      ? `The backups go to ${a.name}. ${list.attached} is detached: it stays in the list with its content, still invoiced.`
      : `The backups go to ${a.name}.`,
    run: () => call(() => clusterService.attachArchive(clusterName, a.name))
  })

  const askDetach = () => setConfirm({
    title: `Detach ${list.attached}?`,
    body: `The cluster is left with no archive. ${list.attached} stays in the list with its content, still invoiced.`,
    run: () => call(() => clusterService.detachArchive(clusterName))
  })

  const askRemove = (a) => setConfirm({
    title: `Remove ${a.name}?`,
    body: `The content of ${a.name} (${summary(a)}) is deleted, then the archive leaves the list.${a.attached ? ' It is attached: the cluster is left with no archive.' : ''} A backup whose only copy is there is lost. This cannot be undone.`,
    confirmText: 'Remove and delete the content',
    run: () => call(() => clusterService.removeArchive(clusterName, a.name))
  })

  const badgeVariant = theme === 'dark' ? 'solid' : 'subtle'

  return (
    <Box width='100%'>
      <Flex justify='space-between' align='center' mb='2'>
        <Text fontSize='sm'>Select an archive to attach it: the backups go to it.</Text>
        <Flex gap='2'>
          {list.attached && <RMButton variant='outline' isDisabled={!canSet} onClick={askDetach}>Detach</RMButton>}
          <RMButton colorScheme='blue' isDisabled={!canSet || list.plugins.length === 0} onClick={() => setForm({})}>Add an archive</RMButton>
        </Flex>
      </Flex>
      {error && <Text color='red.500' fontSize='sm' mb='2'>{error}</Text>}
      <Table size='sm'>
        <Thead>
          <Tr><Th>Attached</Th><Th>Name</Th><Th>Plugin</Th><Th>Repository</Th><Th>Secrets set</Th><Th>Status</Th><Th /></Tr>
        </Thead>
        <Tbody>
          {list.archives.length === 0 && (
            <Tr><Td colSpan={7}><Text fontSize='sm'>No archive: add one.</Text></Td></Tr>
          )}
          {list.archives.map((a) => (
            <Tr key={a.name}>
              <Td>
                <Radio isChecked={a.attached} isDisabled={!canSet || a.attached || a.status === 'removing'} onChange={() => askAttach(a)} />
              </Td>
              <Td><Text fontSize='sm'>{a.name}</Text></Td>
              <Td><Text fontSize='sm'>{a.plugin}</Text></Td>
              <Td><Text fontSize='sm'>{summary(a)}</Text></Td>
              <Td><Text fontSize='sm'>{(a.secretsSet || []).join(', ') || 'none'}</Text></Td>
              <Td>
                {a.status
                  ? <Badge colorScheme={a.status === 'removing' ? 'orange' : 'red'} variant={badgeVariant}>{a.status}</Badge>
                  : a.attached ? <Badge colorScheme='green' variant={badgeVariant}>attached</Badge>
                    : <Badge colorScheme='gray' variant={badgeVariant}>detached</Badge>}
              </Td>
              <Td>
                <Flex gap='1' justify='flex-end'>
                  <RMButton size='xs' variant='outline' isDisabled={!canSet || a.status === 'removing'} onClick={() => setForm({ archive: a })}>Edit</RMButton>
                  <RMButton size='xs' variant='outline' isDisabled={!canSet || a.status === 'removing'} onClick={() => setRename(a)}>Rename</RMButton>
                  <RMButton size='xs' colorScheme='red' variant='outline' isDisabled={!canRemove || a.status === 'removing'} onClick={() => askRemove(a)}>Remove</RMButton>
                </Flex>
              </Td>
            </Tr>
          ))}
        </Tbody>
      </Table>

      {form && (
        <ArchiveFormModal isOpen={!!form} closeModal={() => setForm(null)} plugins={list.plugins}
          archive={form.archive} onSave={saveArchive} />
      )}
      {rename && (
        <TextInputModal isOpen={!!rename} closeModal={() => setRename(null)} title={`Rename ${rename.name}`}
          fieldname='New name' defaultValue={rename.name} regexPattern={NAME_PATTERN}
          onSave={async (value) => {
            const err = await call(() => clusterService.renameArchive(clusterName, rename.name, value))
            if (!err) setRename(null)
          }} />
      )}
      {confirm && (
        <ConfirmModal isOpen={!!confirm} closeModal={() => setConfirm(null)} title={confirm.title}
          body={confirm.body} confirmButtonText={confirm.confirmText || 'Confirm'}
          onConfirmClick={async () => { const run = confirm.run; setConfirm(null); await run() }} />
      )}
    </Box>
  )
}

export default ArchiveListSettings
