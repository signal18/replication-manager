import {
  Box,
  Code,
  FormControl,
  FormErrorMessage,
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
  Stack,
  Text
} from '@chakra-ui/react'
import React, { useEffect, useState } from 'react'
import { useDispatch } from 'react-redux'
import RMButton from '../RMButton'
import { useTheme } from '../../ThemeProvider'
import parentStyles from './styles.module.scss'
import { globalClustersService } from '../../services/globalClustersService'
import { showErrorToast, showSuccessToast } from '../../redux/toastSlice'

// A part of the name: one folder of a store path (InstanceNameForm, config.ValidInstanceNamePart)
const partPattern = /^[A-Za-z0-9._-]{1,63}$/
const partError = (v) =>
  !partPattern.test(v) || v.includes('..') || v.startsWith('.') || v.endsWith('.')
    ? '1 to 63 letters, digits, "-", "_" or ".", not starting or ending with "."'
    : ''

// "Later" holds for this browser session only: the window comes back at the next sign-in
const laterKey = 'instanceNameLater'

// InstanceNameModal asks a sysops or extsysops, once signed in, for the name of an unregistered
// replication-manager while it is not complete (CREDENTIAL_STORES.md "Two levels"). It shows
// nothing to anyone else: the name endpoint answers 403 without the server credentials grant
// on every cluster, and a registered instance is named by its registration.
function InstanceNameModal() {
  const dispatch = useDispatch()
  const { theme } = useTheme()
  const [isOpen, setIsOpen] = useState(false)
  const [form, setForm] = useState({ domain: '', subDomain: '', zone: '' })
  const [saving, setSaving] = useState(false)
  const [touched, setTouched] = useState(false)

  useEffect(() => {
    let later = false
    try {
      later = sessionStorage.getItem(laterKey) === '1'
    } catch (e) {}
    if (later) return
    globalClustersService
      .getInstanceName()
      .then(({ data, status }) => {
        if (status !== 200 || !data || data.registered || data.declined || data.name) return
        setForm({ domain: data.domain || '', subDomain: data.subDomain || '', zone: data.zone || '' })
        setIsOpen(true)
      })
      .catch(() => {})
  }, [])

  const close = () => {
    try {
      sessionStorage.setItem(laterKey, '1')
    } catch (e) {}
    setIsOpen(false)
  }

  const errors = {
    domain: partError(form.domain),
    subDomain: partError(form.subDomain),
    zone: partError(form.zone)
  }
  const valid = !errors.domain && !errors.subDomain && !errors.zone
  const preview = `${form.domain || '<company>'}/${form.subDomain || '<hostname>'}/${form.zone || '<location>'}`

  const save = () => {
    setTouched(true)
    if (!valid) return
    setSaving(true)
    globalClustersService
      .setInstanceName(form)
      .then(({ data }) => {
        dispatch(showSuccessToast({ title: 'Instance name saved', description: data?.name || preview }))
        setIsOpen(false)
      })
      .catch((err) => {
        dispatch(showErrorToast({ title: 'Instance name not saved', description: err?.response?.data || err?.message || '' }))
      })
      .finally(() => setSaving(false))
  }

  // "No thank you, don't ask again": kept on the server, for every sysops and extsysops; the
  // global setting instance-name-declined brings the window back
  const decline = () => {
    setSaving(true)
    globalClustersService
      .setInstanceName({ declined: true })
      .then(() => setIsOpen(false))
      .catch((err) => {
        dispatch(showErrorToast({ title: 'Answer not saved', description: err?.response?.data || err?.message || '' }))
      })
      .finally(() => setSaving(false))
  }

  const field = (key, label, help, placeholder) => (
    <FormControl isInvalid={touched && !!errors[key]}>
      <FormLabel htmlFor={`instance-name-${key}`}>{label}</FormLabel>
      <Input
        id={`instance-name-${key}`}
        type='text'
        value={form[key]}
        placeholder={placeholder}
        maxLength={63}
        onChange={(e) => setForm({ ...form, [key]: e.target.value.trim() })}
      />
      {touched && errors[key] ? <FormErrorMessage>{errors[key]}</FormErrorMessage> : <FormHelperText>{help}</FormHelperText>}
    </FormControl>
  )

  return (
    <Modal size='3xl' isOpen={isOpen} onClose={close} closeOnOverlayClick={false}>
      <ModalOverlay />
      <ModalContent className={theme === 'light' ? parentStyles.modalLightContent : parentStyles.modalDarkContent}>
        <ModalHeader>Version and rotate your secrets</ModalHeader>
        <ModalCloseButton />
        <ModalBody>
          <Stack spacing='5'>
            <Text fontWeight='semibold'>
              To version and rotate your secrets, starting with the admin API user's password, in an
              encrypted local file, Vault, OpenSVC or Kubernetes, name this replication-manager.
            </Text>
            <Text>
              Every replication-manager in your network needs a unique name. It tells this one apart from
              the others wherever they share a place: a key-value store that keeps its secrets (Vault,
              OpenSVC, Kubernetes) and a GitLab that backs up its configuration.
            </Text>
            <Text>
              The name has three parts. The hostname is filled in; enter your company and where this
              replication-manager runs. Only the sysops and extsysops of this instance see this window.
            </Text>
            {field('domain', 'Company', 'Your company, the same for all your replication-managers', 'acme')}
            {field('subDomain', 'Hostname', 'This replication-manager, read from its host at its first start', 'repman1')}
            {field('zone', 'Location', 'Where it runs: an office or a country', 'paris')}
            <Box>
              <Text fontSize='sm'>Its name in the stores:</Text>
              <Code>{preview}</Code>
            </Box>
            <Text fontSize='sm'>
              Choose "No thank you, don't ask again" if you don't plan to version your secrets and rotate
              them on a regular basis, and you're not interested in the security, workload and schema
              monitoring plugins. A sysops can bring this window back in the global settings.
            </Text>
            <Text fontSize='sm'>
              This information stays local until you register this replication-manager with a Cloud18
              account, which activates the community plugins and the versioning of its configuration in
              the Cloud18 GitLab. Until the name is complete, its secrets stay in its local store only.
            </Text>
          </Stack>
        </ModalBody>
        <ModalFooter gap={3} margin='auto' flexWrap='wrap' justifyContent='center'>
          <RMButton
            colorScheme='blue'
            size='medium'
            variant='outline'
            onClick={decline}
            isDisabled={saving}
            title="I don't plan to version my secrets and rotate them on a regular basis, and I'm not interested in the security, workload and schema monitoring plugins."
          >
            No thank you, don't ask again
          </RMButton>
          <RMButton colorScheme='blue' size='medium' variant='outline' onClick={close}>
            Later
          </RMButton>
          <RMButton onClick={save} size='medium' isDisabled={saving}>
            Save
          </RMButton>
        </ModalFooter>
      </ModalContent>
    </Modal>
  )
}

export default InstanceNameModal
