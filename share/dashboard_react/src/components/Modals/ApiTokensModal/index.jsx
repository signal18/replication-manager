import { createColumnHelper } from '@tanstack/react-table'
import React, { useEffect, useMemo, useState } from 'react'
import { useDispatch, useSelector } from 'react-redux'
import {
  Badge,
  Box,
  Checkbox,
  Code,
  FormControl,
  FormErrorMessage,
  FormLabel,
  HStack,
  Input,
  Modal,
  ModalBody,
  ModalCloseButton,
  ModalContent,
  ModalFooter,
  ModalHeader,
  ModalOverlay,
  NumberInput,
  NumberInputField,
  Stack,
  Text,
  Wrap,
  WrapItem
} from '@chakra-ui/react'
import { TbKey, TbTrash, TbEye } from 'react-icons/tb'
import { DataTable } from '../../DataTable'
import RMButton from '../../RMButton'
import RMIconButton from '../../RMIconButton'
import ConfirmModal from '../ConfirmModal'
import GrantCheckList from '../../GrantCheckList'
import parentStyles from '../styles.module.scss'
import { useTheme } from '../../../ThemeProvider'
import { createApiToken, getApiTokens, revokeApiToken } from '../../../redux/clusterSlice'
import { getMonitoredData } from '../../../redux/globalClustersSlice'

// User-issued API tokens (issue #1835). Opened from the User Profile panel: a
// token belongs to the person, not to a cluster. `user` is the logged-in user as
// the navbar holds it, with `grants` and `roles` keyed by cluster name; the grant
// picker offers the union of the grants held across those clusters and the
// cluster picker lists them. The server re-checks everything at creation.

const columnHelper = createColumnHelper()

const fmtDate = (v) => (v && !v.startsWith('0001-') ? new Date(v).toLocaleString() : '-')

// unionGrants flattens the per-cluster grant map into one {grant: true} map.
const unionGrants = (user) => {
  const out = {}
  Object.values(user?.grants || {}).forEach((byGrant) => {
    Object.entries(byGrant || {}).forEach(([g, v]) => {
      if (v) out[g] = true
    })
  })
  return out
}

function TokenDisplayModal({ token, isOpen, closeModal }) {
  const [copied, setCopied] = useState(false)
  const { theme } = useTheme()
  const copy = () => {
    try {
      navigator.clipboard.writeText(token.token)
      setCopied(true)
    } catch (e) {
      setCopied(false)
    }
  }
  return (
    <Modal isOpen={isOpen} onClose={closeModal} size='xl'>
      <ModalOverlay />
      <ModalContent className={theme === 'light' ? parentStyles.modalLightContent : parentStyles.modalDarkContent}>
        <ModalHeader fontSize='md'>API token {token.label}</ModalHeader>
        <ModalCloseButton />
        <ModalBody>
          <Text mb={2} fontSize='sm'>
            Use it as <Code>Authorization: Bearer &lt;token&gt;</Code> or with <Code>replication-manager-cli --api-token</Code>.
          </Text>
          <Code p={2} whiteSpace='pre-wrap' wordBreak='break-all' display='block' fontSize='xs'>
            {token.token}
          </Code>
        </ModalBody>
        <ModalFooter gap={3} margin='auto'>
          <RMButton onClick={copy} size='medium' variant='outline'>
            {copied ? 'Copied' : 'Copy'}
          </RMButton>
          <RMButton onClick={closeModal} size='medium'>
            Close
          </RMButton>
        </ModalFooter>
      </ModalContent>
    </Modal>
  )
}

function CreateTokenModal({ user, isOpen, closeModal, onCreated }) {
  const dispatch = useDispatch()
  const { theme } = useTheme()
  const {
    globalClusters: { monitor }
  } = useSelector((state) => state)
  const [label, setLabel] = useState('')
  const [labelError, setLabelError] = useState('')
  const [acls, setAcls] = useState([]) // selection reported by GrantCheckList (names)
  const [allAcls, setAllAcls] = useState([])
  const [clusters, setClusters] = useState([])
  const [allClusters, setAllClusters] = useState(true)
  const [expireDays, setExpireDays] = useState(monitor?.config?.apiUserTokensDefaultExpireDays ?? 120)
  const [never, setNever] = useState(false)
  const { serviceAcl = [] } = monitor || {}
  const held = useMemo(() => unionGrants(user), [user])
  const clusterNames = useMemo(() => Object.keys(user?.grants || {}).sort(), [user])
  const pickerUser = useMemo(() => ({ grants: held, roles: {} }), [held])

  useEffect(() => {
    if (monitor === null) {
      dispatch(getMonitoredData({}))
    }
  }, [monitor])

  useEffect(() => {
    if (serviceAcl?.length > 0 && allAcls.length === 0) {
      // Only the grants the logged-in user holds somewhere can go into a token.
      const options = serviceAcl.filter((item) => held[item.grant]).map((item) => Object.assign({}, item, { selected: false }))
      setAllAcls(options)
    }
  }, [serviceAcl, held])

  const handleSubmit = () => {
    if (!label.trim()) {
      setLabelError('Label is required')
      return
    }
    setLabelError('')
    // GrantCheckList reports the selection as a list of names (group prefixes or
    // grant names), which is exactly the compact form the server accepts.
    const selected = (acls || []).map((a) => (typeof a === 'string' ? a : a?.grant)).filter(Boolean)
    dispatch(
      createApiToken({
        label: label.trim(),
        grants: selected.join(' '),
        clusters: allClusters ? ['*'] : clusters,
        expireDays: never ? -1 : Number(expireDays) || 0
      })
    ).then((res) => {
      if (res?.payload?.data?.token) {
        onCreated(res.payload.data)
      }
      closeModal()
    })
  }

  return (
    <Modal isOpen={isOpen} onClose={closeModal} size='xl'>
      <ModalOverlay />
      <ModalContent className={theme === 'light' ? parentStyles.modalLightContent : parentStyles.modalDarkContent}>
        <ModalHeader fontSize='md'>Create API token</ModalHeader>
        <ModalCloseButton />
        <ModalBody>
          <Stack spacing={4}>
            <FormControl isInvalid={!!labelError}>
              <FormLabel fontSize='sm'>Label</FormLabel>
              <Input size='sm' value={label} onChange={(e) => setLabel(e.target.value)} placeholder='ci, mcp, laptop…' maxLength={64} />
              <FormErrorMessage>{labelError}</FormErrorMessage>
            </FormControl>
            <FormControl>
              <FormLabel fontSize='sm'>Grants (none selected = every grant you hold)</FormLabel>
              <GrantCheckList grantOptions={allAcls} onChange={setAcls} parentStyles={parentStyles} user={pickerUser} />
            </FormControl>
            <FormControl>
              <FormLabel fontSize='sm'>Clusters</FormLabel>
              <Checkbox size='sm' isChecked={allClusters} onChange={(e) => setAllClusters(e.target.checked)}>
                Every cluster (needed for global settings)
              </Checkbox>
              {!allClusters && (
                <Wrap mt={2}>
                  {clusterNames.map((name) => (
                    <WrapItem key={name}>
                      <Checkbox
                        size='sm'
                        isChecked={clusters.includes(name)}
                        onChange={(e) =>
                          setClusters(e.target.checked ? [...clusters, name] : clusters.filter((c) => c !== name))
                        }>
                        {name}
                      </Checkbox>
                    </WrapItem>
                  ))}
                </Wrap>
              )}
            </FormControl>
            <FormControl>
              <FormLabel fontSize='sm'>Expires in days</FormLabel>
              <HStack>
                <NumberInput size='sm' min={1} value={expireDays} isDisabled={never} onChange={(v) => setExpireDays(v)} maxW='120px'>
                  <NumberInputField />
                </NumberInput>
                <Checkbox size='sm' isChecked={never} onChange={(e) => setNever(e.target.checked)}>
                  Never expires
                </Checkbox>
              </HStack>
            </FormControl>
          </Stack>
        </ModalBody>
        <ModalFooter gap={3} margin='auto'>
          <RMButton colorScheme='blue' size='medium' variant='outline' onClick={closeModal}>
            Cancel
          </RMButton>
          <RMButton onClick={handleSubmit} size='medium'>
            Create
          </RMButton>
        </ModalFooter>
      </ModalContent>
    </Modal>
  )
}

function ApiTokensModal({ isOpen, closeModal, user }) {
  const dispatch = useDispatch()
  const { theme } = useTheme()
  const {
    cluster: { apiTokens },
    globalClusters: { monitor }
  } = useSelector((state) => state)
  const [isCreateOpen, setIsCreateOpen] = useState(false)
  const [shown, setShown] = useState(null)
  const [toRevoke, setToRevoke] = useState(null)
  const enabled = monitor?.config?.apiUserTokens !== false

  useEffect(() => {
    if (isOpen) {
      dispatch(getApiTokens())
    }
  }, [isOpen])

  const columns = useMemo(
    () => [
      columnHelper.accessor((row) => row.label, { cell: (info) => info.getValue(), header: 'Label', id: 'label' }),
      columnHelper.accessor((row) => (row.grants || []).join(' '), { cell: (info) => info.getValue(), header: 'Grants', id: 'grants' }),
      columnHelper.accessor((row) => (row.clusters || []).join(','), { cell: (info) => info.getValue(), header: 'Clusters', id: 'clusters' }),
      columnHelper.accessor((row) => fmtDate(row.expiresAt), { cell: (info) => info.getValue(), header: 'Expires', id: 'expiresAt' }),
      columnHelper.accessor((row) => fmtDate(row.lastUsedAt), { cell: (info) => info.getValue(), header: 'Last used', id: 'lastUsedAt' }),
      columnHelper.accessor(
        (row) =>
          row.revoked ? (
            <Badge colorScheme='red'>revoked</Badge>
          ) : row.expired ? (
            <Badge colorScheme='orange'>expired</Badge>
          ) : (
            <Badge colorScheme='green'>active</Badge>
          ),
        { cell: (info) => info.getValue(), header: 'State', id: 'state' }
      ),
      columnHelper.accessor(
        (row) => (
          <HStack align={'center'} justifyContent={'center'}>
            {row.token && !row.revoked && !row.expired && (
              <RMIconButton tooltip={'show token'} icon={TbEye} onClick={(e) => { e.stopPropagation(); setShown(row) }} />
            )}
            {!row.revoked && (
              <RMIconButton tooltip={'revoke token'} icon={TbTrash} onClick={(e) => { e.stopPropagation(); setToRevoke(row) }} />
            )}
          </HStack>
        ),
        { cell: (info) => info.getValue(), header: 'Actions', id: 'actions' }
      )
    ],
    []
  )

  return (
    <>
      <Modal isOpen={isOpen} onClose={closeModal} size='4xl'>
        <ModalOverlay />
        <ModalContent className={theme === 'light' ? parentStyles.modalLightContent : parentStyles.modalDarkContent}>
          <ModalHeader fontSize='md'>
            <HStack justify='space-between' pr={8}>
              <Text>API tokens of {user?.DisplayName || user?.User || user?.username || ''}</Text>
              {enabled && (
                <RMButton size='small' onClick={() => setIsCreateOpen(true)}>
                  <HStack spacing={1}>
                    <TbKey />
                    <Text fontSize='sm'>New token</Text>
                  </HStack>
                </RMButton>
              )}
            </HStack>
          </ModalHeader>
          <ModalCloseButton />
          <ModalBody pb={4}>
            {enabled ? (
              <Box overflowX='auto'>
                <DataTable key='api-tokens' data={apiTokens || []} columns={columns} />
              </Box>
            ) : (
              <Text p={4} fontSize='sm'>
                API tokens are disabled on this server (api-user-tokens).
              </Text>
            )}
            <Text mt={3} fontSize='xs' color='gray.500'>
              A token carries at most the grants you hold and can be limited to some clusters. It stops working the
              moment it is revoked, when it expires, or when your own grants are removed.
            </Text>
          </ModalBody>
        </ModalContent>
      </Modal>
      {isCreateOpen && (
        <CreateTokenModal user={user} isOpen={isCreateOpen} closeModal={() => setIsCreateOpen(false)} onCreated={(t) => setShown(t)} />
      )}
      {shown && <TokenDisplayModal token={shown} isOpen={!!shown} closeModal={() => setShown(null)} />}
      {toRevoke && (
        <ConfirmModal
          title={`Revoke API token ${toRevoke.label}? Clients using it will be refused immediately.`}
          isOpen={!!toRevoke}
          onConfirmClick={() => {
            dispatch(revokeApiToken({ tokenId: toRevoke.id }))
            setToRevoke(null)
          }}
          closeModal={() => setToRevoke(null)}
        />
      )}
    </>
  )
}

export default ApiTokensModal
