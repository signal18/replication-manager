import {
  Alert,
  AlertIcon,
  Box,
  Checkbox,
  Modal,
  ModalBody,
  ModalCloseButton,
  ModalContent,
  ModalFooter,
  ModalHeader,
  ModalOverlay,
  Text
} from '@chakra-ui/react'
import { useMemo, useState } from 'react'
import { useDispatch, useSelector } from 'react-redux'
import { createColumnHelper } from '@tanstack/react-table'
import { DataTable } from '../DataTable'
import NotFound from '../NotFound'
import TagPill from '../TagPill'
import RMButton from '../RMButton'
import parentStyles from './styles.module.scss'
import { useTheme } from '../../ThemeProvider'
import { clusterService } from '../../services/clusterService'
import { getClusterApps, getClusterData } from '../../redux/clusterSlice'
import { showSuccessToast, showErrorToast } from '../../redux/toastSlice'
import PropTypes from 'prop-types'

const PREVIEW_STATUS_META = {
  importable: { label: 'Importable', colorScheme: 'blue' },
  already_exists: { label: 'Already exists', colorScheme: 'gray' },
  unsupported_same_host: { label: 'Unsupported', colorScheme: 'orange' },
  invalid_peer: { label: 'Invalid peer', colorScheme: 'red' }
}

const APPLY_STATUS_META = {
  imported: { label: 'Imported', colorScheme: 'green' },
  rejected: { label: 'Rejected', colorScheme: 'red' }
}

const rowKey = (host, port) => `${host}:${port}`

function PeerAppImportModal({ clusterName, isOpen, closeModal }) {
  const dispatch = useDispatch()
  const { theme } = useTheme()
  const baseURL = useSelector((state) => state.auth?.baseURL || '')

  const [previewLoading, setPreviewLoading] = useState(false)
  const [applyLoading, setApplyLoading] = useState(false)
  const [previewData, setPreviewData] = useState(null)
  const [selectedApps, setSelectedApps] = useState({})
  const [applyResults, setApplyResults] = useState(null)
  const [inlineError, setInlineError] = useState('')

  const previewApps = previewData?.apps || []
  const selectedCount = Object.values(selectedApps).filter(Boolean).length
  const hasPreview = Boolean(previewData)
  const hasApplyResults = Boolean(applyResults)

  const handleLoadPreview = async () => {
    setPreviewLoading(true)
    setInlineError('')
    setApplyResults(null)
    setSelectedApps({})
    try {
      const { data, status } = await clusterService.previewPeerAppImport(clusterName, baseURL)
      if (status === 200) {
        setPreviewData(data)
      } else {
        setPreviewData(null)
        setInlineError(typeof data === 'string' ? data : 'Failed to load peer app inventory')
      }
    } catch (err) {
      setPreviewData(null)
      setInlineError(String(err?.message || err))
    } finally {
      setPreviewLoading(false)
    }
  }

  const handleToggleSelect = (host, port) => {
    const key = rowKey(host, port)
    setSelectedApps((prev) => ({ ...prev, [key]: !prev[key] }))
  }

  const handleApply = async () => {
    const apps = previewApps
      .filter((app) => app.status === 'importable' && selectedApps[rowKey(app.host, app.port)])
      .map((app) => ({ host: app.host, port: app.port }))

    if (apps.length === 0) return

    setApplyLoading(true)
    setInlineError('')
    try {
      const { data, status } = await clusterService.applyPeerAppImport(clusterName, apps, baseURL)
      if (status === 200) {
        setApplyResults(data)
        setSelectedApps({})

        const importedCount = (data?.apps || []).filter((app) => app.status === 'imported').length
        if (importedCount > 0) {
          dispatch(getClusterApps({ clusterName }))
          dispatch(getClusterData({ clusterName }))
          dispatch(showSuccessToast({
            title: 'App monitor import',
            description: `${importedCount} app${importedCount === 1 ? '' : 's'} imported from peer.`
          }))
        }
        if (importedCount < apps.length) {
          dispatch(showErrorToast({
            title: 'App monitor import',
            description: `${apps.length - importedCount} app${apps.length - importedCount === 1 ? '' : 's'} could not be imported.`
          }))
        }
      } else {
        setInlineError(typeof data === 'string' ? data : 'Failed to apply peer app import')
      }
    } catch (err) {
      setInlineError(String(err?.message || err))
    } finally {
      setApplyLoading(false)
    }
  }

  const handleClose = () => {
    setPreviewData(null)
    setSelectedApps({})
    setApplyResults(null)
    setInlineError('')
    closeModal()
  }

  const columnHelper = useMemo(() => createColumnHelper(), [])
  const columns = useMemo(() => [
    columnHelper.accessor((row) => row, {
      id: 'select',
      header: () => <span />,
      cell: (info) => {
        const app = info.getValue()
        const isImportable = app.status === 'importable'
        return (
          <Checkbox
            isDisabled={!isImportable || applyLoading}
            isChecked={Boolean(selectedApps[rowKey(app.host, app.port)])}
            onChange={() => handleToggleSelect(app.host, app.port)}
          />
        )
      },
      maxWidth: '40'
    }),
    columnHelper.accessor((row) => row.host, {
      id: 'host',
      header: () => <span>Host</span>,
      cell: (info) => info.getValue() || ''
    }),
    columnHelper.accessor((row) => row.port, {
      id: 'port',
      header: () => <span>Port</span>,
      cell: (info) => info.getValue() || '',
      maxWidth: '80'
    }),
    columnHelper.accessor((row) => row.template, {
      id: 'template',
      header: () => <span>Template</span>,
      cell: (info) => info.getValue() || '-'
    }),
    columnHelper.accessor((row) => row.dockerImage, {
      id: 'dockerImage',
      header: () => <span>Docker Image</span>,
      cell: (info) => info.getValue() || '-'
    }),
    columnHelper.accessor((row) => row, {
      id: 'status',
      header: () => <span>Status</span>,
      cell: (info) => {
        const app = info.getValue()
        const meta = PREVIEW_STATUS_META[app.status] || { label: app.status, colorScheme: 'gray' }
        return <TagPill colorScheme={meta.colorScheme} text={meta.label} />
      }
    }),
    columnHelper.accessor((row) => row.reason, {
      id: 'reason',
      header: () => <span>Reason</span>,
      cell: (info) => info.getValue() || ''
    })
  ], [columnHelper, selectedApps, applyLoading])

  const applyColumnHelper = useMemo(() => createColumnHelper(), [])
  const applyColumns = useMemo(() => [
    applyColumnHelper.accessor((row) => row.host, {
      id: 'host',
      header: () => <span>Host</span>,
      cell: (info) => info.getValue() || ''
    }),
    applyColumnHelper.accessor((row) => row.port, {
      id: 'port',
      header: () => <span>Port</span>,
      cell: (info) => info.getValue() || '',
      maxWidth: '80'
    }),
    applyColumnHelper.accessor((row) => row, {
      id: 'status',
      header: () => <span>Status</span>,
      cell: (info) => {
        const app = info.getValue()
        const meta = APPLY_STATUS_META[app.status] || { label: app.status, colorScheme: 'gray' }
        return <TagPill colorScheme={meta.colorScheme} text={meta.label} />
      }
    }),
    applyColumnHelper.accessor((row) => row.reason, {
      id: 'reason',
      header: () => <span>Reason</span>,
      cell: (info) => info.getValue() || ''
    })
  ], [applyColumnHelper])

  return (
    <Modal isOpen={isOpen} onClose={handleClose}>
      <ModalOverlay />
      <ModalContent
        className={theme === 'light' ? parentStyles.modalLightContent : parentStyles.modalDarkContent}
        width='80%'
        maxWidth='none'
        minHeight='300px'
        maxH='90%'
        overflow='hidden'>
        <ModalHeader>Import App Monitor from Peer</ModalHeader>
        <ModalCloseButton />
        <ModalBody overflowY='auto' pb={6}>
          {inlineError && (
            <Alert status='error' borderRadius='md' mb={4}>
              <AlertIcon />
              {inlineError}
            </Alert>
          )}

          {hasPreview && !hasApplyResults && (
            <Text fontSize='sm' mb={4}>
              Peer cluster: {previewData.clusterName} ({previewData.peerUri})
            </Text>
          )}

          {!hasPreview && !hasApplyResults && !previewLoading && !inlineError && (
            <NotFound text='Load the peer app inventory to see importable apps.' />
          )}

          {hasPreview && !hasApplyResults && (
            previewApps.length === 0 ? (
              <NotFound text='No apps found on the peer for this cluster.' />
            ) : (
              <DataTable
                key='PeerAppImportPreview'
                columns={columns}
                data={previewApps}
                cellValueAlign='start'
              />
            )
          )}

          {hasApplyResults && (
            <Box>
              <Text fontSize='sm' fontWeight='bold' mb={2}>Import results</Text>
              <DataTable
                key='PeerAppImportResults'
                columns={applyColumns}
                data={applyResults.apps || []}
                cellValueAlign='start'
              />
            </Box>
          )}
        </ModalBody>

        <ModalFooter gap={3} margin='auto'>
          <RMButton colorScheme='blue' size='medium' variant='outline' onClick={handleClose}>
            {hasApplyResults ? 'Close' : 'Cancel'}
          </RMButton>
          {!hasApplyResults && (
            <RMButton
              size='medium'
              variant='outline'
              onClick={handleLoadPreview}
              isLoading={previewLoading}
              isDisabled={applyLoading}>
              {hasPreview ? 'Reload from Peer' : 'Load from Peer'}
            </RMButton>
          )}
          {hasPreview && !hasApplyResults && (
            <RMButton
              size='medium'
              onClick={handleApply}
              isLoading={applyLoading}
              isDisabled={selectedCount === 0 || previewLoading}>
              Import Selected
            </RMButton>
          )}
          {hasApplyResults && (
            <RMButton
              size='medium'
              variant='outline'
              onClick={handleLoadPreview}
              isLoading={previewLoading}>
              Reload from Peer
            </RMButton>
          )}
        </ModalFooter>
      </ModalContent>
    </Modal>
  )
}

export default PeerAppImportModal

PeerAppImportModal.propTypes = {
  clusterName: PropTypes.string.isRequired,
  isOpen: PropTypes.bool.isRequired,
  closeModal: PropTypes.func.isRequired
}
