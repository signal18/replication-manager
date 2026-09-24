import { createColumnHelper } from '@tanstack/react-table'
import React, { useEffect, useMemo, useState } from 'react'
import { useDispatch, useSelector } from 'react-redux'
import {
  Badge,
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
  Tooltip,
  Wrap,
  WrapItem
} from '@chakra-ui/react'
import { TbKey, TbTrash, TbEye, TbCopy } from 'react-icons/tb'
import { DataTable } from '../../components/DataTable'
import AccordionComponent from '../../components/AccordionComponent'
import RMButton from '../../components/RMButton'
import RMIconButton from '../../components/RMIconButton'
import ConfirmModal from '../../components/Modals/ConfirmModal'
import GrantCheckList from '../../components/GrantCheckList'
import parentStyles from '../../components/Modals/styles.module.scss'
import styles from './styles.module.scss'
import { createApiToken, getApiTokens, revokeApiToken } from '../../redux/clusterSlice'
import { getMonitoredData } from '../../redux/globalClustersSlice'

// User-issued API tokens (issue #1835): a user issues bearer tokens for
// themselves, narrowed to a subset of their own grants and a cluster scope.
// The list is the caller's own tokens; the token string is the owner's, so
// it can be shown again from the eye icon.

const columnHelper = createColumnHelper()

const fmtDate = (v) => (v && !v.startsWith('0001-') ? new Date(v).toLocaleString() : '-')

function TokenDisplayModal({ token, isOpen, closeModal }) {
  const [copied, setCopied] = useState(false)
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
      <ModalContent className={parentStyles.modalContent}>
        <ModalHeader>API token {token.label}</ModalHeader>
        <ModalCloseButton />
        <ModalBody>
          <Text mb={2}>
            Use it as <Code>Authorization: Bearer &lt;token&gt;</Code> or with <Code>replication-manager-cli --api-token</Code>.
          </Text>
          <Code p={2} whiteSpace='pre-wrap' wordBreak='break-all' display='block'>
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
  const {
    globalClusters: { monitor }
  } = useSelector((state) => state)
  const [label, setLabel] = useState('')
  const [labelError, setLabelError] = useState('')
  const [acls, setAcls] = useState([])
  const [allAcls, setAllAcls] = useState([])
  const [clusters, setClusters] = useState([])
  const [allClusters, setAllClusters] = useState(true)
  const [expireDays, setExpireDays] = useState(monitor?.config?.apiUserTokensDefaultExpireDays ?? 120)
  const [never, setNever] = useState(false)
  const { serviceAcl = [] } = monitor || {}
  const clusterNames = monitor?.clusters || []

  useEffect(() => {
    if (monitor === null) {
      dispatch(getMonitoredData({}))
    }
  }, [monitor])

  useEffect(() => {
    if (serviceAcl?.length > 0 && user && allAcls.length === 0) {
      // Only the grants the logged-in user holds can go into a token.
      const held = serviceAcl.filter((item) => user.grants?.[item.grant]).map((item) => Object.assign({}, item, { selected: false }))
      setAllAcls(held)
      setAcls(held)
    }
  }, [serviceAcl, user])

  const handleSubmit = () => {
    if (!label.trim()) {
      setLabelError('Label is required')
      return
    }
    setLabelError('')
    const selected = acls.filter((a) => a.selected).map((a) => a.grant)
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
      <ModalContent className={parentStyles.modalContent}>
        <ModalHeader>Create API token</ModalHeader>
        <ModalCloseButton />
        <ModalBody>
          <Stack spacing={4}>
            <FormControl isInvalid={!!labelError}>
              <FormLabel>Label</FormLabel>
              <Input value={label} onChange={(e) => setLabel(e.target.value)} placeholder='ci, mcp, laptop…' maxLength={64} />
              <FormErrorMessage>{labelError}</FormErrorMessage>
            </FormControl>
            <FormControl>
              <FormLabel>Grants (none selected = every grant you hold)</FormLabel>
              <GrantCheckList grantOptions={allAcls} onChange={setAcls} parentStyles={parentStyles} user={user} />
            </FormControl>
            <FormControl>
              <FormLabel>Clusters</FormLabel>
              <Checkbox isChecked={allClusters} onChange={(e) => setAllClusters(e.target.checked)}>
                Every cluster (needed for global settings)
              </Checkbox>
              {!allClusters && (
                <Wrap mt={2}>
                  {clusterNames.map((name) => (
                    <WrapItem key={name}>
                      <Checkbox
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
              <FormLabel>Expires in days</FormLabel>
              <HStack>
                <NumberInput min={1} value={expireDays} isDisabled={never} onChange={(v) => setExpireDays(v)} maxW='120px'>
                  <NumberInputField />
                </NumberInput>
                <Checkbox isChecked={never} onChange={(e) => setNever(e.target.checked)}>
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

function ApiTokens({ user }) {
  const dispatch = useDispatch()
  const {
    cluster: { apiTokens },
    globalClusters: { monitor }
  } = useSelector((state) => state)
  const [isCreateOpen, setIsCreateOpen] = useState(false)
  const [shown, setShown] = useState(null)
  const [toRevoke, setToRevoke] = useState(null)
  const enabled = monitor?.config?.apiUserTokens !== false

  useEffect(() => {
    dispatch(getApiTokens())
  }, [])

  const columns = useMemo(
    () => [
      columnHelper.accessor((row) => row.label, { cell: (info) => info.getValue(), header: 'Label', id: 'label' }),
      columnHelper.accessor((row) => (row.grants || []).join(' '), { cell: (info) => info.getValue(), header: 'Grants', id: 'grants' }),
      columnHelper.accessor((row) => (row.clusters || []).join(','), { cell: (info) => info.getValue(), header: 'Clusters', id: 'clusters' }),
      columnHelper.accessor((row) => fmtDate(row.createdAt), { cell: (info) => info.getValue(), header: 'Created', id: 'createdAt' }),
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
      <AccordionComponent
        heading={'API TOKENS'}
        allowToggle={false}
        className={styles.accordion}
        panelSX={{ overflowX: 'auto', p: 0 }}
        headerActions={
          enabled ? (
            <Tooltip label='Create API token'>
              <span>
                <RMIconButton icon={TbKey} tooltip={'Create API token'} px='2' variant='outline' onClick={() => setIsCreateOpen(true)} />
              </span>
            </Tooltip>
          ) : undefined
        }
        body={
          enabled ? (
            <DataTable key='api-tokens' data={apiTokens || []} columns={columns} className={styles.table} />
          ) : (
            <Text p={4}>API tokens are disabled on this server (api-user-tokens).</Text>
          )
        }
      />
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

export default ApiTokens
