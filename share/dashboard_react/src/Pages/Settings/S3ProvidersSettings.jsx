import React, { useState } from 'react'
import {
  Box,
  Flex,
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
  Input,
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
  selectClusterS3Providers
} from '../../redux/clusterSlice'
import ConfirmModal from '../../components/Modals/ConfirmModal'
import CommonModal from '../../components/Modals/CommonModal'
import RMButton from '../../components/RMButton'
import PasswordControl from '../../components/PasswordControl'
import styles from './styles.module.scss'

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

  const clusterName = selectedCluster?.name

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
            ...(formData.accessKey.trim() ? { accessKey: formData.accessKey.trim() } : {}),
            ...(formData.secretKey ? { secretKey: formData.secretKey } : {})
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
                    <HStack spacing={2} justify='flex-end'>
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
    </Box>
  )
}

export default S3ProvidersSettings
