import { Box, Button, Flex, HStack, Select, Text, Textarea, VStack } from '@chakra-ui/react'
import React, { useCallback, useEffect, useMemo, useState } from 'react'
import PropTypes from 'prop-types'
import { useDispatch, useSelector } from 'react-redux'
import { refreshAppTemplateRepo } from '../../../../redux/globalClustersSlice'
import { createLocalAppTemplateCopy, deleteAppTemplateContent, previewAppTemplateContent, saveAppTemplateContent } from '../../../../redux/settingsSlice'

function Templates({ clusterName, appConfig, user }) {
  const dispatch = useDispatch()
  const monitor = useSelector((state) => state.globalClusters.monitor)
  const templateMeta = useMemo(() => {
    const meta = Array.isArray(monitor?.serviceTemplateMetadata) ? monitor.serviceTemplateMetadata : []
    if (meta.length > 0) {
      return meta
    }
    const names = Array.isArray(monitor?.serviceTemplates) ? monitor.serviceTemplates : []
    return names.map((name) => ({ name, origin: name?.startsWith('shared/') ? 'shared' : 'repo', editable: false }))
  }, [monitor?.serviceTemplateMetadata, monitor?.serviceTemplates])

  const [selectedTemplate, setSelectedTemplate] = useState(appConfig?.provAppTemplate || '')
  const [content, setContent] = useState('')
  const [isDirty, setIsDirty] = useState(false)
  const [isLoadingContent, setIsLoadingContent] = useState(false)

  useEffect(() => {
    if (!clusterName) return
    dispatch(refreshAppTemplateRepo({ clusterName }))
  }, [clusterName, dispatch])

  useEffect(() => {
    if (!selectedTemplate && appConfig?.provAppTemplate) {
      setSelectedTemplate(appConfig.provAppTemplate)
    }
  }, [appConfig?.provAppTemplate, selectedTemplate])

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

  const selectedMeta = useMemo(() => templateMeta.find((row) => row?.name === selectedTemplate), [templateMeta, selectedTemplate])

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

  const handleSave = useCallback(() => {
    if (!selectedTemplate || !isDirty) return
    dispatch(saveAppTemplateContent({ clusterName, templateName: selectedTemplate, content }))
      .unwrap()
      .then(() => {
        setIsDirty(false)
        dispatch(refreshAppTemplateRepo({ clusterName }))
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
        dispatch(refreshAppTemplateRepo({ clusterName }))
      })
  }, [clusterName, selectedTemplate, dispatch])

  const handleCreateLocalCopy = useCallback(() => {
    if (!selectedTemplate) return
    const suggested = selectedTemplate.startsWith('shared/')
      ? `local/${selectedTemplate.replace(/^shared\//, '')}`
      : `local/${selectedTemplate.replace(/\//g, '-')}`
    const localTemplateName = window.prompt('Enter local template name', suggested)
    if (!localTemplateName) {
      return
    }
    dispatch(createLocalAppTemplateCopy({ clusterName, templateName: selectedTemplate, localTemplateName: localTemplateName.trim() }))
      .unwrap()
      .then(() => {
        dispatch(refreshAppTemplateRepo({ clusterName }))
        setSelectedTemplate(localTemplateName.trim())
      })
  }, [clusterName, selectedTemplate, dispatch])

  const canEdit = Boolean(selectedMeta?.editable)

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
        <Button size='sm' onClick={() => dispatch(refreshAppTemplateRepo({ clusterName }))}>Refresh Template List</Button>
      </Flex>

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
        <Text fontSize='sm' color='gray.500'>
          Origin: {selectedMeta?.origin || 'unknown'} • Editable: {canEdit ? 'yes' : 'no'} • Dirty: {isDirty ? 'yes' : 'no'}
        </Text>
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
    </VStack>
  )
}

Templates.propTypes = {
  clusterName: PropTypes.string.isRequired,
  appConfig: PropTypes.shape({
    provAppTemplate: PropTypes.string
  }),
  user: PropTypes.shape({
    grants: PropTypes.object
  })
}

export default React.memo(Templates)
