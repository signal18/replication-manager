import React, { useMemo, useRef, useState } from 'react'
import {
  Badge,
  Box,
  Checkbox,
  Flex,
  Divider,
  Text,
  Stack,
  Table,
  Thead,
  Tbody,
  Tr,
  Th,
  Td,
  FormControl,
  FormLabel,
  FormErrorMessage,
  Spinner,
  Input,
  Tooltip,
  RadioGroup,
  Radio,
  HStack,
  Select as ChakraSelect
} from '@chakra-ui/react'
import { useDispatch, useSelector } from 'react-redux'
import {
  addS3Provider,
  modifyS3Provider,
  dropS3Provider,
  getS3ProviderReferences,
  previewS3ProviderBulkSync,
  applyS3ProviderBulkSync,
  getClusterData,
  selectClusterS3Providers
} from '../../redux/clusterSlice'
import ConfirmModal from '../../components/Modals/ConfirmModal'
import CommonModal from '../../components/Modals/CommonModal'
import RMButton from '../../components/RMButton'
import PasswordControl from '../../components/PasswordControl'
import SyncDiffTable from '../../components/SyncDiffTable'
import {
  buildBulkApplyDisplaySummary,
  buildEligibleApplyTargetsFromPreview,
  hasPendingPreviewChanges,
} from '../../components/syncDiffUtils'
import {
  buildDefaultSelectedTargetsByKey,
  buildSelectedSyncTargets,
  getReferenceKey,
  getTargetsFingerprint,
  getSelectedTargetsCount,
} from './s3ProviderBulkSyncUtils'

const EMPTY_FORM = {
  name: '',
  providerSource: 'custom',
  providerApp: '',
  endpoint: '',
  region: '',
  accessKey: '',
  secretKey: ''
}

function S3ProvidersSettings({ selectedCluster }) {
  const dispatch = useDispatch()
  const providers = useSelector(selectClusterS3Providers)
  const appS3Providers = useSelector(
    (state) => state.cluster?.clusterData?.appS3Providers ?? []
  )

  const [isFormOpen, setIsFormOpen] = useState(false)
  const [formMode, setFormMode] = useState('add')
  const [formData, setFormData] = useState(EMPTY_FORM)
  const [formErrors, setFormErrors] = useState({})
  const [submitError, setSubmitError] = useState('')
  const [isSubmitting, setIsSubmitting] = useState(false)

  const [pendingDelete, setPendingDelete] = useState(null)
  const [isDeleteOpen, setIsDeleteOpen] = useState(false)
  const [deleteError, setDeleteError] = useState('')

  const [pendingReferencesProvider, setPendingReferencesProvider] = useState(null)
  const [isReferencesOpen, setIsReferencesOpen] = useState(false)
  const [referencesData, setReferencesData] = useState(null)
  const [isReferencesLoading, setIsReferencesLoading] = useState(false)
  const [referencesError, setReferencesError] = useState('')

  const [isBulkSyncOpen, setIsBulkSyncOpen] = useState(false)
  const [bulkSyncProviderName, setBulkSyncProviderName] = useState('')
  const [bulkReferences, setBulkReferences] = useState([])
  const [bulkSelectedTargetsByKey, setBulkSelectedTargetsByKey] = useState({})
  const [isBulkSyncLoading, setIsBulkSyncLoading] = useState(false)
  const [bulkSyncError, setBulkSyncError] = useState('')
  const [bulkNoRefsMessageByProvider, setBulkNoRefsMessageByProvider] = useState({})
  const [bulkPreviewData, setBulkPreviewData] = useState(null)
  const [isBulkPreviewLoading, setIsBulkPreviewLoading] = useState(false)
  const [bulkPreviewError, setBulkPreviewError] = useState('')
  const [bulkApplyData, setBulkApplyData] = useState(null)
  const [isBulkApplyLoading, setIsBulkApplyLoading] = useState(false)
  const [bulkApplyError, setBulkApplyError] = useState('')
  const [bulkApplyMeta, setBulkApplyMeta] = useState({ selectedTotal: 0, excludedFromApply: 0 })
  const [bulkSuccessMessageByProvider, setBulkSuccessMessageByProvider] = useState({})
  const [bulkPreviewTargetsFingerprint, setBulkPreviewTargetsFingerprint] = useState('')

  const bulkSelectionVersionRef = useRef(0)
  const bulkPreviewRequestIdRef = useRef(0)

  const clusterName = selectedCluster?.name

  const bulkSelectedTargets = useMemo(
    () => buildSelectedSyncTargets(bulkReferences, bulkSelectedTargetsByKey),
    [bulkReferences, bulkSelectedTargetsByKey]
  )

  const bulkSelectedCount = useMemo(
    () => getSelectedTargetsCount(bulkReferences, bulkSelectedTargetsByKey),
    [bulkReferences, bulkSelectedTargetsByKey]
  )

  const bulkSelectedTargetsFingerprint = useMemo(
    () => getTargetsFingerprint(bulkSelectedTargets),
    [bulkSelectedTargets]
  )

  const openAddForm = () => {
    setFormMode('add')
    setFormData(EMPTY_FORM)
    setFormErrors({})
    setSubmitError('')
    setIsFormOpen(true)
  }

  const openEditForm = (provider) => {
    setFormMode('edit')
    setFormData({
      name: provider.name,
      providerSource: provider.providerSource || 'custom',
      providerApp: provider.providerApp || '',
      endpoint: provider.endpoint || '',
      region: provider.region || '',
      accessKey: '',
      secretKey: ''
    })
    setFormErrors({})
    setSubmitError('')
    setIsFormOpen(true)
  }

  const closeForm = () => {
    setIsFormOpen(false)
  }

  const openReferencesModal = async (providerName) => {
    if (isReferencesLoading) return
    if (!clusterName) {
      setReferencesError('No cluster selected')
      return
    }
    setPendingReferencesProvider(providerName)
    setReferencesData(null)
    setReferencesError('')
    setIsReferencesOpen(true)
    setIsReferencesLoading(true)
    try {
      const { data } = await dispatch(getS3ProviderReferences({ clusterName, providerName })).unwrap()
      if (!data || typeof data !== 'object' || Array.isArray(data) || typeof data.providerName !== 'string') {
        setReferencesError('Invalid response from server')
        return
      }
      if (data.providerName !== providerName) {
        setReferencesError('Invalid response from server: provider mismatch')
        return
      }
      setReferencesData({
        providerName: data.providerName || '',
        referenceCount: typeof data.referenceCount === 'number' ? data.referenceCount : 0,
        references: Array.isArray(data.references) ? data.references : [],
      })
    } catch (err) {
      const msg = err?.errorMessage || err?.message || String(err)
      setReferencesError(msg)
    } finally {
      setIsReferencesLoading(false)
    }
  }

  const closeReferencesModal = () => {
    setIsReferencesOpen(false)
    setPendingReferencesProvider(null)
    setReferencesData(null)
    setReferencesError('')
  }

  const closeBulkSyncModal = () => {
    setIsBulkSyncOpen(false)
    setBulkSyncProviderName('')
    setBulkReferences([])
    setBulkSelectedTargetsByKey({})
    setBulkSyncError('')
    setBulkPreviewData(null)
    setBulkPreviewError('')
    setBulkApplyData(null)
    setBulkApplyError('')
    setBulkApplyMeta({ selectedTotal: 0, excludedFromApply: 0 })
    setBulkPreviewTargetsFingerprint('')
  }

  const openBulkSyncModal = async (providerName) => {
    if (isBulkSyncLoading || isBulkPreviewLoading || isBulkApplyLoading) return
    if (!clusterName) {
      setBulkSyncError('No cluster selected')
      return
    }
    setBulkSyncProviderName(providerName)
    setIsBulkSyncLoading(true)
    setBulkSyncError('')
    setBulkPreviewData(null)
    setBulkPreviewError('')
    setBulkApplyData(null)
    setBulkApplyError('')
    setBulkSuccessMessageByProvider((prev) => ({ ...prev, [providerName]: '' }))
    try {
      const { data } = await dispatch(getS3ProviderReferences({ clusterName, providerName })).unwrap()
      if (!data || typeof data !== 'object' || Array.isArray(data) || typeof data.providerName !== 'string') {
        setBulkSyncError('Invalid response from server')
        return
      }
      if (data.providerName !== providerName) {
        setBulkSyncError('Invalid response from server: provider mismatch')
        return
      }
      const references = Array.isArray(data.references) ? data.references : []
      const referenceCount = typeof data.referenceCount === 'number' ? data.referenceCount : references.length
      if (referenceCount > 0 && references.length === 0) {
        setBulkSyncError('Invalid response from server: missing references list')
        return
      }
      if (referenceCount === 0) {
        setBulkNoRefsMessageByProvider((prev) => ({
          ...prev,
          [providerName]: 'No mounts reference this provider.',
        }))
        return
      }
      setBulkNoRefsMessageByProvider((prev) => ({ ...prev, [providerName]: '' }))
      setBulkReferences(references)
      setBulkSelectedTargetsByKey(buildDefaultSelectedTargetsByKey(references))
      bulkSelectionVersionRef.current += 1
      setBulkPreviewTargetsFingerprint('')
      setIsBulkSyncOpen(true)
    } catch (err) {
      const msg = err?.errorMessage || err?.message || String(err)
      setBulkSyncError(msg)
    } finally {
      setIsBulkSyncLoading(false)
    }
  }

  const handleBulkToggleTarget = (ref, checked) => {
    const key = getReferenceKey(ref)
    setBulkSelectedTargetsByKey((prev) => ({
      ...prev,
      [key]: checked,
    }))
    // Selection changes invalidate existing preview/apply snapshots.
    setBulkPreviewData(null)
    setBulkPreviewError('')
    setBulkApplyData(null)
    setBulkApplyError('')
    setBulkApplyMeta({ selectedTotal: 0, excludedFromApply: 0 })
    setBulkPreviewTargetsFingerprint('')
    bulkSelectionVersionRef.current += 1
  }

  const validateBulkPreviewResponse = (data, providerName, expectedTargets) => {
    const allowedStatuses = new Set(['will_change', 'no_change', 'provider_missing', 'error'])
    if (!data || typeof data !== 'object' || Array.isArray(data)) {
      return { ok: false, error: 'Invalid response from server' }
    }
    if (data.providerName !== providerName) {
      return { ok: false, error: 'Invalid response from server: provider mismatch' }
    }
    if (!data.summary || typeof data.summary !== 'object' || Array.isArray(data.summary)) {
      return { ok: false, error: 'Invalid response from server: missing summary' }
    }
    const s = data.summary
    if (![s.total, s.willChange, s.unchanged, s.failed].every((v) => typeof v === 'number' && Number.isFinite(v) && v >= 0)) {
      return { ok: false, error: 'Invalid response from server: malformed preview summary' }
    }
    if (!Array.isArray(data.results)) {
      return { ok: false, error: 'Invalid response from server: missing preview results' }
    }
    const expectedTotal = expectedTargets.length
    if (s.total !== expectedTotal || data.results.length !== expectedTotal) {
      return { ok: false, error: 'Invalid response from server: preview target count mismatch' }
    }
    if (!data.results.every((r) => r && typeof r === 'object' && allowedStatuses.has(r.status))) {
      return { ok: false, error: 'Invalid response from server: unknown preview status' }
    }

    const toKey = (t) => JSON.stringify([String(t?.appId || '').trim(), String(t?.mountName || '').trim()])
    const expectedKeys = new Set(expectedTargets.map(toKey))
    const resultKeys = new Set()
    for (const r of data.results) {
      const key = toKey(r?.target)
      if (!key || key === '["",""]') {
        return { ok: false, error: 'Invalid response from server: malformed preview result target' }
      }
      if (!expectedKeys.has(key)) {
        return { ok: false, error: 'Invalid response from server: unexpected preview result target' }
      }
      if (resultKeys.has(key)) {
        return { ok: false, error: 'Invalid response from server: duplicate preview result target' }
      }
      resultKeys.add(key)
    }
    if (resultKeys.size !== expectedKeys.size) {
      return { ok: false, error: 'Invalid response from server: preview result target mismatch' }
    }

    const willChangeFromResults = data.results.filter((r) => r.status === 'will_change').length
    const unchangedFromResults = data.results.filter((r) => r.status === 'no_change').length
    const failedFromResults = data.results.filter((r) => r.status === 'provider_missing' || r.status === 'error').length
    if (willChangeFromResults !== s.willChange || unchangedFromResults !== s.unchanged || failedFromResults !== s.failed) {
      return { ok: false, error: 'Invalid response from server: preview summary count mismatch' }
    }
    if (s.willChange + s.unchanged + s.failed !== s.total) {
      return { ok: false, error: 'Invalid response from server: preview summary total mismatch' }
    }
    return { ok: true, data }
  }

  const validateBulkApplyResponse = (data, providerName, expectedTargets) => {
    const allowedStatuses = new Set(['changed', 'unchanged', 'provider_missing', 'error'])
    if (!data || typeof data !== 'object' || Array.isArray(data)) {
      return { ok: false, error: 'Invalid response from server' }
    }
    if (data.providerName !== providerName) {
      return { ok: false, error: 'Invalid response from server: provider mismatch' }
    }
    if (!data.summary || typeof data.summary !== 'object' || Array.isArray(data.summary)) {
      return { ok: false, error: 'Invalid response from server: missing summary' }
    }
    const s = data.summary
    if (![s.total, s.changed, s.unchanged, s.failed].every((v) => typeof v === 'number' && Number.isFinite(v) && v >= 0)) {
      return { ok: false, error: 'Invalid response from server: malformed apply summary' }
    }
    if (!Array.isArray(data.results)) {
      return { ok: false, error: 'Invalid response from server: missing apply results' }
    }
    const expectedTotal = expectedTargets.length
    if (s.total !== expectedTotal || data.results.length !== expectedTotal) {
      return { ok: false, error: 'Invalid response from server: apply target count mismatch' }
    }
    if (!data.results.every((r) => r && typeof r === 'object' && allowedStatuses.has(r.status))) {
      return { ok: false, error: 'Invalid response from server: unknown apply status' }
    }

    const toKey = (t) => JSON.stringify([String(t?.appId || '').trim(), String(t?.mountName || '').trim()])
    const expectedKeys = new Set(expectedTargets.map(toKey))
    const resultKeys = new Set()
    for (const r of data.results) {
      const key = toKey(r?.target)
      if (!key || key === '["",""]') {
        return { ok: false, error: 'Invalid response from server: malformed apply result target' }
      }
      if (!expectedKeys.has(key)) {
        return { ok: false, error: 'Invalid response from server: unexpected apply result target' }
      }
      if (resultKeys.has(key)) {
        return { ok: false, error: 'Invalid response from server: duplicate apply result target' }
      }
      resultKeys.add(key)
    }
    if (resultKeys.size !== expectedKeys.size) {
      return { ok: false, error: 'Invalid response from server: apply result target mismatch' }
    }

    const changedFromResults = data.results.filter((r) => r.status === 'changed').length
    const unchangedFromResults = data.results.filter((r) => r.status === 'unchanged').length
    const failedFromResults = data.results.filter((r) => r.status === 'provider_missing' || r.status === 'error').length
    if (changedFromResults !== s.changed || unchangedFromResults !== s.unchanged || failedFromResults !== s.failed) {
      return { ok: false, error: 'Invalid response from server: apply summary count mismatch' }
    }
    if (s.changed + s.unchanged + s.failed !== s.total) {
      return { ok: false, error: 'Invalid response from server: apply summary total mismatch' }
    }
    return { ok: true, data }
  }

  const handleBulkPreview = async () => {
    if (!clusterName || !bulkSyncProviderName || bulkSelectedTargets.length === 0) return
    const previewTargets = bulkSelectedTargets
    const previewSelectionVersion = bulkSelectionVersionRef.current
    const previewRequestId = ++bulkPreviewRequestIdRef.current
    const previewFingerprint = getTargetsFingerprint(previewTargets)
    setIsBulkPreviewLoading(true)
    setBulkPreviewError('')
    setBulkApplyData(null)
    setBulkApplyError('')
    setBulkApplyMeta({ selectedTotal: 0, excludedFromApply: 0 })
    setBulkPreviewTargetsFingerprint('')
    try {
      const { data } = await dispatch(
        previewS3ProviderBulkSync({
          clusterName,
          providerName: bulkSyncProviderName,
          targets: previewTargets,
        })
      ).unwrap()
      if (previewRequestId !== bulkPreviewRequestIdRef.current) {
        return
      }
      if (previewSelectionVersion !== bulkSelectionVersionRef.current) {
        setBulkPreviewError('Selection changed while preview was loading. Please run preview again.')
        return
      }
      const validated = validateBulkPreviewResponse(data, bulkSyncProviderName, previewTargets)
      if (!validated.ok) {
        setBulkPreviewError(validated.error)
        return
      }
      setBulkPreviewData(validated.data)
      setBulkPreviewTargetsFingerprint(previewFingerprint)
    } catch (err) {
      const msg = err?.errorMessage || err?.message || String(err)
      setBulkPreviewError(msg)
    } finally {
      setIsBulkPreviewLoading(false)
    }
  }

  const handleBulkApply = async () => {
    if (!clusterName || !bulkSyncProviderName || bulkSelectedTargets.length === 0) return
    if (!bulkPreviewData || !bulkPreviewTargetsFingerprint || bulkPreviewTargetsFingerprint !== bulkSelectedTargetsFingerprint) {
      setBulkApplyError('Selection changed since preview. Please run preview again before applying.')
      return
    }
    if (!hasPendingPreviewChanges(bulkPreviewData)) {
      setBulkApplyError('No pending changes. Selected mounts already match the provider.')
      return
    }
    setIsBulkApplyLoading(true)
    setBulkApplyError('')
    try {
      const applyTargets = buildEligibleApplyTargetsFromPreview(bulkSelectedTargets, bulkPreviewData)
      const excludedFromApply = Math.max(0, bulkSelectedTargets.length - applyTargets.length)
      setBulkApplyMeta({ selectedTotal: bulkSelectedTargets.length, excludedFromApply })
      if (applyTargets.length === 0) {
        setBulkApplyError('No eligible mounts to apply. Resolve preview errors first.')
        return
      }
      const { data } = await dispatch(
        applyS3ProviderBulkSync({
          clusterName,
          providerName: bulkSyncProviderName,
          targets: applyTargets,
        })
      ).unwrap()
      const validated = validateBulkApplyResponse(data, bulkSyncProviderName, applyTargets)
      if (!validated.ok) {
        setBulkApplyError(validated.error)
        return
      }
      setBulkApplyData(validated.data)

      const changed = validated.data.summary.changed
      const unchanged = validated.data.summary.unchanged
      const failed = validated.data.summary.failed
      if (changed > 0) {
        await dispatch(getClusterData({ clusterName }))
      }
      if (failed === 0) {
        setBulkSuccessMessageByProvider((prev) => ({
          ...prev,
          [bulkSyncProviderName]: `Sync applied (changed: ${changed}, unchanged: ${unchanged}).`,
        }))
      }
    } catch (err) {
      const msg = err?.errorMessage || err?.message || String(err)
      setBulkApplyError(msg)
    } finally {
      setIsBulkApplyLoading(false)
    }
  }

  const handleFieldChange = (field, value) => {
    setFormData((prev) => ({ ...prev, [field]: value }))
    setFormErrors((prev) => ({ ...prev, [field]: '' }))
    setSubmitError('')
  }

  const handleModeChange = (value) => {
    setFormData((prev) => ({
      ...EMPTY_FORM,
      name: prev.name,
      providerSource: value
    }))
    setFormErrors({})
    setSubmitError('')
  }

  const validateForm = () => {
    const errors = {}
    if (formMode === 'add' && !formData.name.trim()) {
      errors.name = 'Name is required'
    }
    if (formData.providerSource === 'app') {
      if (!formData.providerApp.trim()) {
        errors.providerApp = 'Sibling app is required'
      }
    } else {
      if (!formData.endpoint.trim()) {
        errors.endpoint = 'Endpoint is required'
      }
    }
    return errors
  }

  const handleSubmit = async () => {
    const errors = validateForm()
    if (Object.keys(errors).length > 0) {
      setFormErrors(errors)
      return
    }

    const payload =
      formData.providerSource === 'app'
        ? {
            name: formData.name.trim(),
            providerSource: 'app',
            providerApp: formData.providerApp.trim()
          }
        : {
            name: formData.name.trim(),
            providerSource: 'custom',
            endpoint: formData.endpoint.trim(),
            region: formData.region.trim(),
            ...(formData.accessKey.trim() ? { accesskey: formData.accessKey.trim() } : {}),
            ...(formData.secretKey ? { secretkey: formData.secretKey } : {})
          }

    setIsSubmitting(true)
    setSubmitError('')

    try {
      let result
      if (formMode === 'add') {
        result = await dispatch(addS3Provider({ clusterName, payload })).unwrap()
      } else {
        const { name, ...modifyPayload } = payload
        result = await dispatch(
          modifyS3Provider({ clusterName, name: formData.name, payload: modifyPayload })
        ).unwrap()
      }
      closeForm()
    } catch (err) {
      const msg = err?.errorMessage || err?.message || String(err)
      setSubmitError(msg)
    } finally {
      setIsSubmitting(false)
    }
  }

  const openDeleteConfirm = (providerName) => {
    setPendingDelete(providerName)
    setDeleteError('')
    setIsDeleteOpen(true)
  }

  const handleConfirmDelete = async () => {
    if (!pendingDelete) return
    const name = pendingDelete
    setIsDeleteOpen(false)
    setPendingDelete(null)
    try {
      await dispatch(dropS3Provider({ clusterName, name })).unwrap()
    } catch (err) {
      const msg = err?.errorMessage || err?.message || String(err)
      setDeleteError(msg)
    }
  }

  const getReferenceStatusColor = (status) => {
    if (status === 'matches_provider') return 'green'
    if (status === 'customized') return 'blue'
    if (status === 'provider_missing') return 'red'
    return 'gray'
  }

  const getReferenceStatusLabel = (status) => {
    if (status === 'matches_provider') return 'Matches provider'
    if (status === 'customized') return 'Customized'
    if (status === 'provider_missing') return 'Provider missing'
    return 'Unknown'
  }

  const getApplyStatusColor = (status) => {
    if (status === 'changed') return 'blue'
    if (status === 'unchanged') return 'green'
    if (status === 'provider_missing' || status === 'error') return 'red'
    return 'gray'
  }

  const getApplyStatusLabel = (status) => {
    if (status === 'changed') return 'Changed'
    if (status === 'unchanged') return 'Unchanged'
    if (status === 'provider_missing') return 'Provider missing'
    if (status === 'error') return 'Sync error'
    return 'Unknown'
  }

  const formBody = (
    <Stack spacing={4} pb={2}>
      {formMode === 'add' && (
        <FormControl isRequired isInvalid={!!formErrors.name}>
          <FormLabel>Name</FormLabel>
          <Input
            value={formData.name}
            onChange={(e) => handleFieldChange('name', e.target.value)}
            placeholder='e.g. my-s3-provider'
          />
          {formErrors.name && <FormErrorMessage>{formErrors.name}</FormErrorMessage>}
        </FormControl>
      )}
      {formMode === 'edit' && (
        <Box>
          <Text fontSize='sm' fontWeight='semibold'>
            Provider: <Text as='span' fontWeight='normal'>{formData.name}</Text>
          </Text>
        </Box>
      )}

      <FormControl>
        <FormLabel>Mode</FormLabel>
        <RadioGroup
          value={formData.providerSource}
          onChange={handleModeChange}
        >
          <HStack spacing={6}>
            <Radio value='custom'>Custom Endpoint</Radio>
            <Radio value='app'>Sibling App</Radio>
          </HStack>
        </RadioGroup>
      </FormControl>

      {formData.providerSource === 'app' ? (
        <FormControl isRequired isInvalid={!!formErrors.providerApp}>
          <FormLabel>Sibling App</FormLabel>
          <ChakraSelect
            value={formData.providerApp}
            onChange={(e) => handleFieldChange('providerApp', e.target.value)}
            placeholder='Select app provider'
          >
            {appS3Providers.map((prov) => (
              <option key={prov} value={prov}>
                {prov}
              </option>
            ))}
          </ChakraSelect>
          {formErrors.providerApp && (
            <FormErrorMessage>{formErrors.providerApp}</FormErrorMessage>
          )}
        </FormControl>
      ) : (
        <>
          <FormControl isRequired isInvalid={!!formErrors.endpoint}>
            <FormLabel>Endpoint</FormLabel>
            <Input
              value={formData.endpoint}
              onChange={(e) => handleFieldChange('endpoint', e.target.value)}
              placeholder='e.g. https://s3.example.com'
            />
            {formErrors.endpoint && (
              <FormErrorMessage>{formErrors.endpoint}</FormErrorMessage>
            )}
          </FormControl>
          <FormControl>
            <FormLabel>Region</FormLabel>
            <Input
              value={formData.region}
              onChange={(e) => handleFieldChange('region', e.target.value)}
              placeholder='e.g. us-east-1'
            />
          </FormControl>
          <FormControl>
            <FormLabel>Access Key</FormLabel>
            <Input
              value={formData.accessKey}
              onChange={(e) => handleFieldChange('accessKey', e.target.value)}
              placeholder='Access key ID'
            />
          </FormControl>
          <FormControl>
            <FormLabel>
              Secret Key{formMode === 'edit' && (
                <Text as='span' fontSize='xs' fontWeight='normal' ml={2} opacity={0.6}>
                  (leave blank to keep existing)
                </Text>
              )}
            </FormLabel>
            <PasswordControl
              noControl
              value={formData.secretKey}
              onChange={(e) => handleFieldChange('secretKey', e.target.value)}
              placeholder={formMode === 'edit' ? '••••••••' : 'Secret key'}
              autoComplete='new-password'
              required={false}
            />
          </FormControl>
        </>
      )}

      {submitError && (
        <Box p={2} borderRadius='md' bg='red.50' borderWidth={1} borderColor='red.200'>
          <Text fontSize='sm' color='red.600'>
            {submitError}
          </Text>
        </Box>
      )}

      <Flex justify='flex-end' gap={3} pt={2}>
        <RMButton variant='ghost' onClick={closeForm} isDisabled={isSubmitting}>
          Cancel
        </RMButton>
        <RMButton
          colorScheme='blue'
          onClick={handleSubmit}
          isLoading={isSubmitting}
          isDisabled={isSubmitting}
        >
          {formMode === 'add' ? 'Add Provider' : 'Save Changes'}
        </RMButton>
      </Flex>
    </Stack>
  )

  return (
    <Box p={4}>
      <Flex justify='flex-end' mb={4}>
        <RMButton colorScheme='blue' onClick={openAddForm}>
          Add Provider
        </RMButton>
      </Flex>

      {deleteError && (
        <Box mb={3} p={2} borderRadius='md' bg='red.50' borderWidth={1} borderColor='red.200'>
          <Text fontSize='sm' color='red.600'>
            Delete failed: {deleteError}
          </Text>
        </Box>
      )}

      {bulkSyncError && !isBulkSyncOpen && (
        <Box mb={3} p={2} borderRadius='md' bg='red.50' borderWidth={1} borderColor='red.200'>
          <Text fontSize='sm' color='red.600'>
            Bulk sync failed to start: {bulkSyncError}
          </Text>
        </Box>
      )}

      {providers.length === 0 ? (
        <Text fontSize='sm' opacity={0.6}>
          No S3 providers configured for this cluster.
        </Text>
      ) : (
        <Box overflowX='auto'>
          <Table size='sm' variant='simple'>
            <Thead>
              <Tr>
                <Th>Name</Th>
                <Th>Mode</Th>
                <Th>Endpoint / App</Th>
                <Th>Region</Th>
                <Th>References</Th>
                <Th />
              </Tr>
            </Thead>
            <Tbody>
              {providers.map((p) => (
                <Tr key={p.name}>
                  <Td fontWeight='medium'>{p.name}</Td>
                  <Td>{p.providerSource === 'app' ? 'Sibling App' : 'Custom'}</Td>
                  <Td>
                    {p.providerSource === 'app'
                      ? p.providerApp || '—'
                      : p.endpoint || '—'}
                  </Td>
                  <Td>{p.region || '—'}</Td>
                  <Td>
                    <HStack spacing={2}>
                      <RMButton size='xs' variant='outline' onClick={() => openReferencesModal(p.name)}>
                        View references
                      </RMButton>
                    </HStack>
                  </Td>
                  <Td>
                    <Stack spacing={1} align='flex-end'>
                      <HStack spacing={2} justify='flex-end'>
                        <Tooltip
                          label={bulkNoRefsMessageByProvider[p.name] || ''}
                          isDisabled={!bulkNoRefsMessageByProvider[p.name]}
                        >
                          <Box>
                            <RMButton
                              size='xs'
                              variant='outline'
                              onClick={() => openBulkSyncModal(p.name)}
                              isLoading={isBulkSyncLoading && bulkSyncProviderName === p.name}
                              isDisabled={
                                (isBulkSyncLoading && bulkSyncProviderName !== p.name) ||
                                isBulkPreviewLoading ||
                                isBulkApplyLoading
                              }
                            >
                              Sync mounts
                            </RMButton>
                          </Box>
                        </Tooltip>
                        <RMButton size='xs' variant='outline' onClick={() => openEditForm(p)}>
                          Edit
                        </RMButton>
                        <RMButton
                          size='xs'
                          colorScheme='red'
                          variant='outline'
                          onClick={() => openDeleteConfirm(p.name)}
                        >
                          Delete
                        </RMButton>
                      </HStack>
                      {!!bulkNoRefsMessageByProvider[p.name] && (
                        <Text fontSize='xs' color='gray.500'>
                          {bulkNoRefsMessageByProvider[p.name]}
                        </Text>
                      )}
                      {!!bulkSuccessMessageByProvider[p.name] && (
                        <Text fontSize='xs' color='green.600'>
                          {bulkSuccessMessageByProvider[p.name]}
                        </Text>
                      )}
                    </Stack>
                  </Td>
                </Tr>
              ))}
            </Tbody>
          </Table>
        </Box>
      )}

      <CommonModal
        isOpen={isFormOpen}
        closeModal={closeForm}
        title={formMode === 'add' ? 'Add S3 Provider' : 'Edit S3 Provider'}
        size='md'
        body={formBody}
      />

      <ConfirmModal
        isOpen={isDeleteOpen}
        closeModal={() => {
          setIsDeleteOpen(false)
          setPendingDelete(null)
        }}
        title='Delete S3 Provider'
        body={`Are you sure you want to delete provider "${pendingDelete}"? This action cannot be undone.`}
        onConfirmClick={handleConfirmDelete}
        confirmButtonText='Delete'
        confirmButtonProps={{ colorScheme: 'red' }}
      />

      <CommonModal
        isOpen={isReferencesOpen}
        closeModal={closeReferencesModal}
        title={`References: ${pendingReferencesProvider || ''}`}
        size='lg'
        body={
          <Box>
            {isReferencesLoading && (
              <Flex justify='center' py={8}>
                <Spinner />
              </Flex>
            )}
            {referencesError && !isReferencesLoading && (
              <Box p={3} borderRadius='md' bg='red.50' borderWidth='1' borderColor='red.200'>
                <Text fontSize='sm' color='red.600'>
                  Failed to load references: {referencesError}
                </Text>
              </Box>
            )}
            {!isReferencesLoading && !referencesError && referencesData && (
              <Box>
                <Text fontSize='sm' mb={3}>
                  <Text as='span' fontWeight='semibold'>Reference count: </Text>
                  <Text as='span'>{referencesData.referenceCount}</Text>
                </Text>
                {referencesData.referenceCount === 0 ? (
                  <Text fontSize='sm' opacity={0.6}>
                    No app S3 mounts reference this provider.
                  </Text>
                ) : (
                  <Table size='sm' variant='simple'>
                    <Thead>
                      <Tr>
                        <Th>App</Th>
                        <Th>Mount</Th>
                        <Th>Status</Th>
                        <Th>Endpoint</Th>
                        <Th>Region</Th>
                        <Th>Bucket</Th>
                      </Tr>
                    </Thead>
                    <Tbody>
                      {(referencesData.references || []).map((ref, idx) => (
                        <Tr key={`${ref.appId}-${ref.mountName}-${idx}`}>
                          <Td fontWeight='medium'>{ref.appName || ref.appId}</Td>
                          <Td>{ref.mountName}</Td>
                          <Td>
                            <Badge
                              colorScheme={getReferenceStatusColor(ref.status)}
                            >
                              {getReferenceStatusLabel(ref.status)}
                            </Badge>
                          </Td>
                          <Td>{ref.fields?.endpoint || '—'}</Td>
                          <Td>{ref.fields?.region || '—'}</Td>
                          <Td>{ref.fields?.bucket || '—'}</Td>
                        </Tr>
                      ))}
                    </Tbody>
                  </Table>
                )}
              </Box>
            )}
          </Box>
        }
      />

      <CommonModal
        isOpen={isBulkSyncOpen}
        closeModal={closeBulkSyncModal}
        title={`Sync mounts: ${bulkSyncProviderName || ''}`}
        size='4xl'
        body={
          <Stack spacing={4}>
            <Text fontSize='sm'>
              <Text as='span' fontWeight='semibold'>Selected mounts: </Text>
              <Text as='span'>{bulkSelectedCount}</Text>
              <Text as='span'> / {bulkReferences.length}</Text>
            </Text>

            <Box borderWidth='1px' borderRadius='md' p={3}>
              <Table size='sm' variant='simple'>
                <Thead>
                  <Tr>
                    <Th w='40px' />
                    <Th>App</Th>
                    <Th>Mount</Th>
                    <Th>Status</Th>
                    <Th>Endpoint</Th>
                    <Th>Region</Th>
                  </Tr>
                </Thead>
                <Tbody>
                  {bulkReferences.map((ref, idx) => {
                    const key = getReferenceKey(ref)
                    const checked = !!bulkSelectedTargetsByKey[key]
                    return (
                      <Tr key={`${key}-${idx}`}>
                        <Td>
                          <Checkbox
                            isChecked={checked}
                            isDisabled={isBulkPreviewLoading || isBulkApplyLoading}
                            onChange={(e) => handleBulkToggleTarget(ref, e.target.checked)}
                          />
                        </Td>
                        <Td fontWeight='medium'>{ref.appName || ref.appId}</Td>
                        <Td>{ref.mountName}</Td>
                        <Td>
                          <Badge colorScheme={getReferenceStatusColor(ref.status)}>
                            {getReferenceStatusLabel(ref.status)}
                          </Badge>
                        </Td>
                        <Td>{ref.fields?.endpoint || '—'}</Td>
                        <Td>{ref.fields?.region || '—'}</Td>
                      </Tr>
                    )
                  })}
                </Tbody>
              </Table>
            </Box>

            <HStack justify='space-between'>
              <Text fontSize='xs' color='gray.500'>
                All mounts are selected by default. Deselect any mount you want to exclude.
              </Text>
              <RMButton
                size='sm'
                onClick={handleBulkPreview}
                isDisabled={bulkSelectedCount === 0 || isBulkPreviewLoading || isBulkApplyLoading}
                isLoading={isBulkPreviewLoading}
              >
                Preview Sync
              </RMButton>
            </HStack>

            {bulkPreviewError && (
              <Box p={3} borderRadius='md' bg='red.50' borderWidth='1' borderColor='red.200'>
                <Text fontSize='sm' color='red.600'>
                  Preview failed: {bulkPreviewError}
                </Text>
              </Box>
            )}

            {bulkPreviewData && (
              <Stack spacing={3}>
                <Divider />
                <Text fontSize='sm'>
                  <Text as='span' fontWeight='semibold'>Preview summary: </Text>
                  Selected: {bulkPreviewData.summary?.total || 0} |
                  {' '}Will change: {bulkPreviewData.summary?.willChange || 0} |
                  {' '}In sync: {bulkPreviewData.summary?.unchanged || 0} |
                  {' '}Errors: {bulkPreviewData.summary?.failed || 0}
                </Text>
                <SyncDiffTable results={bulkPreviewData.results || []} />

                {!hasPendingPreviewChanges(bulkPreviewData) && (
                  <Box p={2} borderRadius='md' bg='gray.50' borderWidth='1' borderColor='gray.200'>
                    <Text fontSize='sm' color='gray.700'>
                      No provider-managed changes are pending for the selected mounts.
                    </Text>
                  </Box>
                )}

                {(bulkPreviewData.summary?.failed || 0) > 0 && (
                  <Box p={2} borderRadius='md' bg='orange.50' borderWidth='1' borderColor='orange.200'>
                    <Text fontSize='sm' color='orange.700'>
                      Rows with preview errors or missing providers will be skipped during apply.
                    </Text>
                  </Box>
                )}

                <HStack justify='flex-end'>
                  <RMButton
                    colorScheme='blue'
                    onClick={handleBulkApply}
                    isDisabled={bulkSelectedCount === 0 || isBulkApplyLoading || !hasPendingPreviewChanges(bulkPreviewData)}
                    isLoading={isBulkApplyLoading}
                  >
                    Apply Sync
                  </RMButton>
                </HStack>
              </Stack>
            )}

            {bulkApplyError && (
              <Box p={3} borderRadius='md' bg='red.50' borderWidth='1' borderColor='red.200'>
                <Text fontSize='sm' color='red.600'>
                  Apply failed: {bulkApplyError}
                </Text>
              </Box>
            )}

            {bulkApplyData && (
              <Stack spacing={3}>
                <Divider />
                {(() => {
                  const summary = buildBulkApplyDisplaySummary({
                    selectedTotal: bulkApplyMeta.selectedTotal || bulkSelectedCount,
                    applySummary: bulkApplyData.summary,
                    excludedFromApply: bulkApplyMeta.excludedFromApply,
                  })
                  return (
                    <Text fontSize='sm'>
                      <Text as='span' fontWeight='semibold'>Apply summary: </Text>
                      Selected: {summary.total} |
                      {' '}Changed: {summary.changed} |
                      {' '}Unchanged: {summary.unchanged} |
                      {' '}Failed: {summary.failed}
                    </Text>
                  )
                })()}
                {(bulkApplyData.summary?.failed || 0) > 0 || (bulkApplyMeta.excludedFromApply || 0) > 0 ? (
                  <Box p={2} borderRadius='md' bg='orange.50' borderWidth='1' borderColor='orange.200'>
                    <Text fontSize='sm' color='orange.700'>
                      Partial success: some mounts were synced, but one or more failed or were skipped. Review failed rows below.
                      {(bulkApplyMeta.excludedFromApply || 0) > 0 && (
                        <Text as='span'> Skipped before apply: {bulkApplyMeta.excludedFromApply}.</Text>
                      )}
                    </Text>
                  </Box>
                ) : null}
                <Table size='sm' variant='simple'>
                  <Thead>
                    <Tr>
                      <Th>App</Th>
                      <Th>Mount</Th>
                      <Th>Status</Th>
                      <Th>Details</Th>
                    </Tr>
                  </Thead>
                  <Tbody>
                    {(bulkApplyData.results || []).map((result, idx) => (
                      <Tr key={`${result?.target?.appId}-${result?.target?.mountName}-${idx}`}>
                        <Td>{result?.target?.appId || '—'}</Td>
                        <Td>{result?.target?.mountName || '—'}</Td>
                        <Td>
                          <Badge colorScheme={getApplyStatusColor(result?.status)}>
                            {getApplyStatusLabel(result?.status)}
                          </Badge>
                        </Td>
                        <Td>
                          <Text fontSize='xs' color={result?.status === 'error' || result?.status === 'provider_missing' ? 'red.500' : 'gray.600'}>
                            {result?.errorMessage || (result?.changesApplied || []).join(', ') || 'No changes applied.'}
                          </Text>
                        </Td>
                      </Tr>
                    ))}
                  </Tbody>
                </Table>
              </Stack>
            )}
          </Stack>
        }
      />
    </Box>
  )
}

export default S3ProvidersSettings
