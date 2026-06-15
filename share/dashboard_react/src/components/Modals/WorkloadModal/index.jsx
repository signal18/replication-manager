import React, { useMemo, useState } from 'react'
import {
  Modal, ModalBody, ModalCloseButton, ModalContent, ModalHeader, ModalOverlay,
  HStack, Button, Tooltip, Badge, Wrap, WrapItem, Text, Box
} from '@chakra-ui/react'
import { useSelector, useDispatch } from 'react-redux'
import { DataTable } from '../../DataTable'
import { createColumnHelper } from '@tanstack/react-table'
import NotFound from '../../NotFound'
import parentStyles from '../styles.module.scss'
import { useTheme } from '../../../ThemeProvider'
import { clusterService } from '../../../services/clusterService'
import { showSuccessToast, showErrorToast } from '../../../redux/toastSlice'

function WorkloadModal({ isOpen, closeModal }) {
  const { theme } = useTheme()
  const dispatch = useDispatch()
  const { isMobile, isTablet, isDesktop } = useSelector((state) => state.common)
  const clusterData = useSelector((state) => state.cluster.clusterData)
  const baseURL = useSelector((state) => state.auth?.baseURL || '')
  const [fixing, setFixing] = useState({})

  const allStates = clusterData?.workloadStates || []

  const tags = useMemo(() => {
    const seen = new Map()
    for (const s of allStates) {
      const key = (s.ErrKey || '').split('@')[0]
      if (!key.startsWith('WTAG')) continue
      if (seen.has(key)) continue
      const desc = s.ErrDesc || ''
      const label = desc.replace(/^Server \S+ uses /, '').replace(/^Server \S+ optimizer: /, '').replace(/ \(.*/, '')
      seen.set(key, { key, label, desc })
    }
    return [...seen.values()]
  }, [allStates])

  const statesArray = useMemo(
    () => allStates.filter((s) => !(s.ErrKey || '').startsWith('WTAG')),
    [allStates]
  )

  // Build a lookup from err_key → RemediationEntry from the workload remediation plan.
  const remediationByKey = useMemo(() => {
    const map = {}
    for (const entry of clusterData?.workloadRemediations?.remediations || []) {
      if (!map[entry.err_key]) map[entry.err_key] = entry
    }
    return map
  }, [clusterData?.workloadRemediations])

  const handleFix = async (errKey) => {
    setFixing((prev) => ({ ...prev, [errKey]: true }))
    try {
      const entry = remediationByKey[errKey]
      const fix = entry?.fixes?.[0]

      let status, data
      if (fix?.type === 'settings_switch' && fix?.url) {
        ;({ status, data } = await clusterService.switchClusterSetting(fix.url, baseURL))
      } else {
        return
      }

      if (status === 200) {
        dispatch(showSuccessToast({
          title: `Fix applied: ${errKey}`,
          description: 'Fix applied successfully.',
        }))
      } else {
        dispatch(showErrorToast({
          title: `Fix failed: ${errKey}`,
          description: typeof data === 'string' ? data : 'Unexpected error',
        }))
      }
    } catch (err) {
      dispatch(showErrorToast({ title: `Fix failed: ${errKey}`, description: String(err) }))
    } finally {
      setFixing((prev) => ({ ...prev, [errKey]: false }))
    }
  }

  const columnHelper = createColumnHelper()
  const columns = useMemo(() => [
    columnHelper.accessor((row) => row, {
      id: 'desc',
      header: () => <span>Description</span>,
      cell: (info) => {
        const row = info.getValue()
        const errKey = (row.ErrKey || '').split('@')[0]
        const desc = row.ErrDesc?.replace(/,(?!\s)/g, ', ') || ''
        const entry = remediationByKey[errKey]

        if (!entry?.auto_fixable) return <span>{desc}</span>

        return (
          <HStack spacing={2} align='start'>
            <Tooltip label='Risk: safe' placement='top'>
              <Button
                size='xs'
                colorScheme='green'
                flexShrink={0}
                isLoading={!!fixing[errKey]}
                onClick={() => handleFix(errKey)}>
                Enable
              </Button>
            </Tooltip>
            <span>{desc}</span>
          </HStack>
        )
      },
    }),
    columnHelper.accessor((row) => row.ServerUrl, {
      id: 'server',
      cell: (info) => info.getValue() || '',
      header: () => <span>Server</span>,
      maxWidth: '200',
    }),
    columnHelper.accessor((row) => row.ErrKey, {
      id: 'code',
      cell: (info) => info.getValue() || '',
      header: () => <span>Code</span>,
      maxWidth: '120',
    }),
  ], [fixing, remediationByKey, baseURL])

  return (
    <Modal isOpen={isOpen} onClose={closeModal}>
      <ModalOverlay />
      <ModalContent
        className={theme === 'light' ? parentStyles.modalLightContent : parentStyles.modalDarkContent}
        width={isDesktop ? '80%' : isTablet ? '97%' : '99%'}
        maxWidth='none'
        minHeight='300px'
        maxH='90%'
        overflow='hidden'>
        <ModalHeader style={{ background: 'var(--chakra-colors-purple-500)', color: 'white' }}>
          Workload ({tags.length > 0 ? `${tags.length} tags` : ''}{tags.length > 0 && statesArray.length > 0 ? ', ' : ''}{statesArray.length > 0 ? `${statesArray.length} findings` : ''})
        </ModalHeader>
        <ModalCloseButton color='white' />
        <ModalBody overflowY='auto' pb={6}>
          {tags.length > 0 && (
            <Box mb={4}>
              <Text fontSize='sm' fontWeight='bold' mb={2}>Feature Tags</Text>
              <Wrap spacing={2}>
                {tags.map((t) => (
                  <WrapItem key={t.key}>
                    <Tooltip label={t.desc} placement='top'>
                      <Badge colorScheme='purple' variant='subtle' px={2} py={1} borderRadius='md'>
                        {t.label}
                      </Badge>
                    </Tooltip>
                  </WrapItem>
                ))}
              </Wrap>
            </Box>
          )}
          {statesArray.length === 0 && tags.length === 0 ? (
            <NotFound text='No active workload findings' />
          ) : statesArray.length === 0 ? null : (
            <DataTable
              key='WorkloadStates'
              columns={columns}
              data={statesArray}
              cellValueAlign='start'
            />
          )}
        </ModalBody>
      </ModalContent>
    </Modal>
  )
}

export default WorkloadModal
