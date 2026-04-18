import {
  Alert,
  AlertDescription,
  AlertIcon,
  Badge,
  Box,
  Button,
  Flex,
  HStack,
  Modal,
  ModalBody,
  ModalCloseButton,
  ModalContent,
  ModalHeader,
  ModalOverlay,
  Select,
  Spinner,
  Text,
  Textarea,
  VStack
} from '@chakra-ui/react'
import React, { useCallback, useEffect, useMemo, useState } from 'react'
import PropTypes from 'prop-types'
import { useDispatch, useSelector } from 'react-redux'
import Markdown from 'react-markdown'
import { getAppTemplateStructureGuide, refreshAppTemplateRepo } from '../../../../redux/globalClustersSlice'
import { createLocalAppTemplateCopy, deleteAppTemplateContent, previewAppTemplateContent, saveAppTemplateContent } from '../../../../redux/settingsSlice'

function Templates({ clusterName, appConfig, user }) {
  const dispatch = useDispatch()
  const monitor = useSelector((state) => state.globalClusters.monitor)
  const templateRepoError = useSelector((state) => state.globalClusters.templateRepoError)
  const templateGuideError = useSelector((state) => state.globalClusters.templateGuideError)
  const templateMeta = useMemo(() => {
    const meta = Array.isArray(monitor?.serviceTemplateMetadata) ? monitor.serviceTemplateMetadata : []
    if (meta.length > 0) {
      return meta
    }
    const names = Array.isArray(monitor?.serviceTemplates) ? monitor.serviceTemplates : []
    return names.map((name) => ({ name, origin: 'repo', scope: 'global', editable: false }))
  }, [monitor?.serviceTemplateMetadata, monitor?.serviceTemplates])

  const [selectedTemplate, setSelectedTemplate] = useState(appConfig?.provAppTemplate || '')
  const [content, setContent] = useState('')
  const [isDirty, setIsDirty] = useState(false)
  const [isLoadingContent, setIsLoadingContent] = useState(false)
  const [isGuideOpen, setIsGuideOpen] = useState(false)
  const [isGuideLoading, setIsGuideLoading] = useState(false)
  const [guideContent, setGuideContent] = useState('')

  useEffect(() => {
    if (!clusterName) return
    dispatch(refreshAppTemplateRepo({ clusterName, silent: true }))
  }, [clusterName, dispatch])

  useEffect(() => {
    if (!selectedTemplate && appConfig?.provAppTemplate) {
      setSelectedTemplate(appConfig.provAppTemplate)
    }
  }, [appConfig?.provAppTemplate, selectedTemplate])

  const loadTemplateContent = useCallback((forceRefresh = false) => {
    if (!selectedTemplate) return
    if (isDirty) {
      const proceed = window.confirm('You have unsaved changes. Continue and discard local edits?')
      if (!proceed) {
        return
      }
    }
    setIsLoadingContent(true)
    dispatch(previewAppTemplateContent({ clusterName, templateName: selectedTemplate, forceRefresh }))
      .unwrap()
      .then(({ data }) => {
        setContent(data?.content || '')
        setIsDirty(false)
      })
      .finally(() => setIsLoadingContent(false))
  }, [clusterName, selectedTemplate, isDirty, dispatch])

  const selectedMeta = useMemo(() => templateMeta.find((row) => row?.name === selectedTemplate), [templateMeta, selectedTemplate])
  const isExampleTemplate = selectedTemplate === 'shared/dummy'

  useEffect(() => {
    if (!selectedTemplate) {
      return
    }
    if (isDirty) {
      return
    }
    if (content !== '') {
      return
    }
    loadTemplateContent(false)
  }, [selectedTemplate, content, isDirty, loadTemplateContent])

  const handleSave = useCallback(() => {
    if (!selectedTemplate || !isDirty) return
    dispatch(saveAppTemplateContent({ clusterName, templateName: selectedTemplate, content }))
      .unwrap()
      .then(() => {
        setIsDirty(false)
        dispatch(refreshAppTemplateRepo({ clusterName, silent: true }))
      })
  }, [clusterName, selectedTemplate, content, isDirty, dispatch])

  const handleDeleteLocal = useCallback(() => {
    if (!selectedTemplate) return
    const proceed = window.confirm(`Delete local template "${selectedTemplate}"? This action cannot be undone.`)
    if (!proceed) {
      return
    }
    dispatch(deleteAppTemplateContent({ clusterName, templateName: selectedTemplate }))
      .unwrap()
      .then(() => {
        setContent('')
        setIsDirty(false)
        dispatch(refreshAppTemplateRepo({ clusterName, silent: true }))
      })
  }, [clusterName, selectedTemplate, dispatch])

  const handleCreateLocalCopy = useCallback(() => {
    if (!selectedTemplate) return
    const isDummySource = selectedTemplate === 'shared/dummy'
    const suggested = isDummySource ? `local/${(appConfig?.appHost || 'my-app').replace(/\s+/g, '-').toLowerCase()}` : selectedTemplate.replace(/\//g, '-')
    const localTemplateName = window.prompt(
      isDummySource
        ? 'Enter local template name (must not end with "dummy")'
        : 'Enter local template name',
      suggested
    )
    if (!localTemplateName) {
      return
    }
    const trimmedLocalTemplateName = localTemplateName.trim()
    const localTemplateBaseName = trimmedLocalTemplateName.split('/').filter(Boolean).pop()?.toLowerCase()
    if (isDummySource && localTemplateBaseName === 'dummy') {
      window.alert('Please rename dummy template to a specific name before creating local copy.')
      return
    }
    dispatch(createLocalAppTemplateCopy({ clusterName, templateName: selectedTemplate, localTemplateName: trimmedLocalTemplateName }))
      .unwrap()
      .then(() => {
        dispatch(refreshAppTemplateRepo({ clusterName, silent: true }))
        setSelectedTemplate(trimmedLocalTemplateName)
      })
  }, [clusterName, selectedTemplate, dispatch, appConfig?.appHost])

  const canEdit = Boolean(selectedMeta?.editable)

  const openTemplateGuide = useCallback(() => {
    setIsGuideOpen(true)
    if (guideContent) {
      return
    }
    setIsGuideLoading(true)
    dispatch(getAppTemplateStructureGuide({ clusterName, silent: true }))
      .unwrap()
      .then(({ data }) => {
        setGuideContent(data?.content || '')
      })
      .finally(() => setIsGuideLoading(false))
  }, [clusterName, dispatch, guideContent])

  const onTemplateChange = useCallback((e) => {
    const nextTemplate = e.target.value
    if (isDirty) {
      const proceed = window.confirm('You have unsaved changes. Switch template and discard edits?')
      if (!proceed) {
        return
      }
    }
    setSelectedTemplate(nextTemplate)
    setContent('')
    setIsDirty(false)
  }, [isDirty])

  return (
    <VStack align='stretch' spacing={4} w='100%'>
      <Flex justifyContent='space-between' alignItems='center'>
        <Text fontSize='lg' fontWeight='600'>App Template Manager</Text>
        <HStack>
          <Button size='sm' variant='outline' onClick={openTemplateGuide}>Template Structure Guide</Button>
          <Button size='sm' onClick={() => dispatch(refreshAppTemplateRepo({ clusterName, forceRefresh: true }))}>Refresh Template List</Button>
        </HStack>
      </Flex>

      {templateRepoError && (
        <Alert status='error'>
          <AlertIcon />
          <AlertDescription>{templateRepoError}</AlertDescription>
        </Alert>
      )}

      <HStack alignItems='flex-end' spacing={3}>
        <Box flex={1}>
          <Text mb={1}>Template</Text>
          <Select value={selectedTemplate} onChange={onTemplateChange}>
            <option value=''>Select template</option>
            {templateMeta.map((row) => (
              <option key={row.name} value={row.name}>{row.name}</option>
            ))}
          </Select>
        </Box>
        <Button size='sm' onClick={() => loadTemplateContent(false)} isDisabled={!selectedTemplate || isLoadingContent}>Preview</Button>
        <Button size='sm' onClick={() => loadTemplateContent(true)} isDisabled={!selectedTemplate || isLoadingContent}>Refresh Content</Button>
      </HStack>

      {selectedTemplate && (
        <HStack spacing={2} alignItems='center' flexWrap='wrap'>
          <Text fontSize='sm' color='gray.500'>
            Scope: {selectedMeta?.scope || 'global'} • Origin: {selectedMeta?.origin || 'unknown'} • Editable: {canEdit ? 'yes' : 'no'} • Dirty: {isDirty ? 'yes' : 'no'}
          </Text>
          {isExampleTemplate && (
            <Badge colorScheme='purple' variant='subtle'>
              Example
            </Badge>
          )}
        </HStack>
      )}

      <Textarea
        minH='360px'
        value={content}
        onChange={(e) => {
          setContent(e.target.value)
          setIsDirty(true)
        }}
        isDisabled={!selectedTemplate || !canEdit}
        placeholder={selectedTemplate ? (canEdit ? 'Edit local template content...' : 'Read-only template (create/save local copy to edit)') : 'Select a template to preview'}
      />

      <HStack justifyContent='flex-end'>
        <Button size='sm' onClick={() => loadTemplateContent(false)} isDisabled={!selectedTemplate || !isDirty}>Discard Changes</Button>
        <Button size='sm' onClick={handleCreateLocalCopy} isDisabled={!selectedTemplate || canEdit || !user?.grants?.['app-deployment']}>Create Local Copy</Button>
        <Button size='sm' colorScheme='blue' onClick={handleSave} isDisabled={!selectedTemplate || !isDirty || !canEdit || !user?.grants?.['app-deployment']}>Save Local Template</Button>
        <Button size='sm' colorScheme='red' variant='outline' onClick={handleDeleteLocal} isDisabled={!selectedTemplate || !canEdit || !user?.grants?.['app-deployment']}>Delete Local Template</Button>
      </HStack>

      <Modal isOpen={isGuideOpen} onClose={() => setIsGuideOpen(false)} size='4xl' isCentered>
        <ModalOverlay />
        <ModalContent>
          <ModalHeader>Template Structure Guide</ModalHeader>
          <ModalCloseButton />
          <ModalBody maxH='70vh' overflowY='auto' pb={6}>
            {isGuideLoading && (
              <HStack>
                <Spinner size='sm' />
                <Text>Loading guide...</Text>
              </HStack>
            )}
            {!isGuideLoading && templateGuideError && !guideContent && (
              <Alert status='error'>
                <AlertIcon />
                <AlertDescription>{templateGuideError}</AlertDescription>
              </Alert>
            )}
            {!isGuideLoading && guideContent && (
              <Markdown>{guideContent}</Markdown>
            )}
          </ModalBody>
        </ModalContent>
      </Modal>
    </VStack>
  )
}

Templates.propTypes = {
  clusterName: PropTypes.string.isRequired,
  appConfig: PropTypes.shape({
    provAppTemplate: PropTypes.string,
    appHost: PropTypes.string
  }),
  user: PropTypes.shape({
    grants: PropTypes.object
  })
}

export default React.memo(Templates)
