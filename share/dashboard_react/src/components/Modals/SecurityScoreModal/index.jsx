import React, { useMemo, useState } from 'react'
import {
  Modal, ModalBody, ModalCloseButton, ModalContent, ModalHeader, ModalOverlay,
  Box, Grid, GridItem, Text, Badge, HStack, VStack, Divider,
  Button, Tooltip
} from '@chakra-ui/react'
import { useSelector, useDispatch } from 'react-redux'
import { DataTable } from '../../DataTable'
import { createColumnHelper } from '@tanstack/react-table'
import parentStyles from '../styles.module.scss'
import { useTheme } from '../../../ThemeProvider'
import { clusterService } from '../../../services/clusterService'
import { showSuccessToast, showErrorToast } from '../../../redux/toastSlice'

// SEC codes that support one-click automated fix via POST /security/fix-state/{errKey}
const AUTO_FIXABLE = new Set(['SEC0100', 'SEC0102', 'SEC0104', 'SEC0107', 'SEC0112', 'SEC0109', 'SEC0110', 'SEC0111'])

// Risk level for each auto-fixable code — shown in the tooltip
const FIX_RISK = {
  SEC0100: 'safe',
  SEC0102: 'safe',
  SEC0104: 'safe',
  SEC0107: 'safe',
  SEC0112: 'safe — installs audit plugin at runtime, no restart',
  SEC0109: 'disruptive — triggers rolling restart',
  SEC0110: 'disruptive — triggers rolling restart',
  SEC0111: 'disruptive — triggers rolling restart',
}

// Label map for the 17 security check tags
const CHECK_LABELS = {
  hasSSL:              'SSL/TLS transport encryption',
  hasZeroSSL:          'Require secure transport (zero plain-text)',
  hasTableEncryption:  'At-rest table encryption',
  hasBinlogEncryption: 'Binary log encryption',
  hasTmpEncryption:    'Temporary files encryption',
  hasBackupEncryption: 'Backup encryption',
  hasAuditPlugins:     'Audit plugin active',
  noEmptyPassword:     'No empty-password accounts',
  hasPrepareStatement: 'Prepared statements supported',
  hasStrongPwd:        'Strong password validation plugin',
  hasProxies:          'Proxy layer configured',
  hasParsecPlugins:    'PARSEC authentication (MariaDB 11.6+)',
  hasPasswordRotation: 'Password rotation (default_password_lifetime > 0)',
  noClearPwdConfigs:   'No clear-text passwords in DB server config files',
  noClearPwdHistory:   'No clear-text passwords in shell/MySQL history',
  noClearPwdBinlogs:   'No clear-text passwords in binary logs',
  hasLastLTS:          'Running a supported LTS release',
}

const GRADE_COLOR = { A: 'green', B: 'teal', C: 'yellow', D: 'orange', F: 'red' }

function SecurityScoreModal({ isOpen, closeModal }) {
  const { theme } = useTheme()
  const dispatch = useDispatch()
  const { isMobile, isTablet, isDesktop } = useSelector((state) => state.common)
  const clusterData = useSelector((state) => state.cluster.clusterData)
  const baseURL = useSelector((state) => state.auth?.baseURL || '')
  const [fixing, setFixing] = useState({})

  const score = clusterData?.securityScore
  const statesArray = clusterData?.securityStates || []
  const clusterName = clusterData?.name

  const handleFix = async (errKey) => {
    setFixing((prev) => ({ ...prev, [errKey]: true }))
    try {
      const { status, data } = await clusterService.fixSecState(clusterName, errKey, baseURL)
      if (status === 200) {
        dispatch(showSuccessToast({
          title: `Fix applied: ${errKey}`,
          description: FIX_RISK[errKey]?.includes('disruptive')
            ? 'Config deployed. Rolling restart in progress…'
            : 'Fix applied successfully.',
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
  const stateColumns = useMemo(() => [
    columnHelper.accessor((row) => row, {
      id: 'desc',
      header: () => <span>Finding</span>,
      cell: (info) => {
        const row = info.getValue()
        const errKey = row.ErrKey
        const desc = row.ErrDesc?.replace(/,(?!\s)/g, ', ') || ''
        if (!AUTO_FIXABLE.has(errKey)) return <span>{desc}</span>
        const risk = FIX_RISK[errKey] || 'safe'
        const isDisruptive = risk.includes('disruptive')
        return (
          <HStack spacing={2} align='start'>
            <Tooltip label={`Risk: ${risk}`} placement='top'>
              <Button
                size='xs'
                colorScheme={isDisruptive ? 'orange' : 'green'}
                flexShrink={0}
                isLoading={!!fixing[errKey]}
                onClick={() => handleFix(errKey)}>
                Fix
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
  ], [fixing, clusterName, baseURL])

  if (!score) return null

  const gradeColor = GRADE_COLOR[score.grade] || 'gray'
  const checks = Object.entries(CHECK_LABELS).map(([key, label]) => ({
    label,
    pass: score[key] === true,
  }))

  return (
    <Modal isOpen={isOpen} onClose={closeModal} size='xl'>
      <ModalOverlay />
      <ModalContent
        className={theme === 'light' ? parentStyles.modalLightContent : parentStyles.modalDarkContent}
        width={isDesktop ? '75%' : isTablet ? '97%' : '99%'}
        maxWidth='none'
        maxH='90%'
        overflow='hidden'>
        <ModalHeader style={{ background: `var(--chakra-colors-${gradeColor}-500)`, color: 'white' }}>
          Security Score — {score.score}/100 &nbsp;
          <Badge fontSize='1.2em' colorScheme={gradeColor} variant='solid'>{score.grade}</Badge>
        </ModalHeader>
        <ModalCloseButton color='white' />
        <ModalBody overflowY='auto' pb={6}>
          {/* 17 check grid */}
          <Grid templateColumns={isDesktop ? 'repeat(2, 1fr)' : '1fr'} gap={2} mb={4}>
            {checks.map(({ label, pass }) => (
              <GridItem key={label}>
                <HStack spacing={2}>
                  <Badge colorScheme={pass ? 'green' : 'red'} minW='40px' textAlign='center'>
                    {pass ? 'PASS' : 'FAIL'}
                  </Badge>
                  <Text fontSize='sm'>{label}</Text>
                </HStack>
              </GridItem>
            ))}
          </Grid>

          {/* Open security states */}
          {statesArray.length > 0 && (
            <>
              <Divider my={3} />
              <Text fontWeight='bold' mb={2}>Open Security Findings ({statesArray.length})</Text>
              <DataTable
                key='SecurityStates'
                columns={stateColumns}
                data={statesArray}
                cellValueAlign='start'
              />
            </>
          )}
        </ModalBody>
      </ModalContent>
    </Modal>
  )
}

export default SecurityScoreModal
