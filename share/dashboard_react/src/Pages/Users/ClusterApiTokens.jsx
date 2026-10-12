import { createColumnHelper } from '@tanstack/react-table'
import React, { useEffect, useMemo, useState } from 'react'
import { useDispatch, useSelector } from 'react-redux'
import { Badge, HStack, Text, VStack } from '@chakra-ui/react'
import { TbTrash } from 'react-icons/tb'
import { DataTable } from '../../components/DataTable'
import AccordionComponent from '../../components/AccordionComponent'
import TabItems from '../../components/TabItems'
import RMIconButton from '../../components/RMIconButton'
import ConfirmModal from '../../components/Modals/ConfirmModal'
import { getClusterApiTokens, revokeApiToken } from '../../redux/clusterSlice'
import styles from './styles.module.scss'

// The API tokens covering this cluster, whoever owns them (issue #1983). Shown to
// a holder of token-manage on the cluster, the grant GET /api/clusters/{name}/tokens
// requires. Revoking another user's token needs token-manage on EVERY cluster the
// token covers: the server refuses otherwise and its message names the cluster.
// Two tabs: Active (usable, revocable) and Archive (revoked or expired, kept by the
// server for the retention period as the audit trail, read-only).

const columnHelper = createColumnHelper()

const fmtDate = (v) => (v && !v.startsWith('0001-') ? new Date(v).toLocaleString() : '-')

function ClusterApiTokens({ selectedCluster }) {
  const dispatch = useDispatch()
  const {
    cluster: { clusterApiTokens }
  } = useSelector((state) => state)
  const [toRevoke, setToRevoke] = useState(null)
  const clusterName = selectedCluster?.name
  // Only a list loaded for this very cluster: another cluster's list stays hidden
  // while this one loads, and is never shown after this one fails.
  const tokens = clusterApiTokens?.clusterName === clusterName ? clusterApiTokens.tokens : []
  const [tabIndex, setTabIndex] = useState(0)
  const active = useMemo(() => tokens.filter((t) => !t.revoked && !t.expired), [tokens])
  const archive = useMemo(() => tokens.filter((t) => t.revoked || t.expired), [tokens])

  useEffect(() => {
    if (clusterName) {
      dispatch(getClusterApiTokens({ clusterName }))
    }
  }, [clusterName])

  const columns = useMemo(
    () => [
      columnHelper.accessor((row) => row.user, { cell: (info) => info.getValue(), header: 'Owner', id: 'user' }),
      columnHelper.accessor((row) => row.label, { cell: (info) => info.getValue(), header: 'Label', id: 'label' }),
      columnHelper.accessor((row) => (row.clusters || []).join(','), { cell: (info) => info.getValue(), header: 'Clusters', id: 'clusters' }),
      columnHelper.accessor((row) => (row.grants || []).join(' ') || 'every grant of the owner', {
        cell: (info) => info.getValue(),
        header: 'Grants',
        id: 'grants'
      }),
      columnHelper.accessor((row) => fmtDate(row.createdAt), { cell: (info) => info.getValue(), header: 'Created', id: 'createdAt' }),
      columnHelper.accessor(
        (row) => (
          <VStack spacing={0} align='start'>
            <Text>{fmtDate(row.lastUsedAt)}</Text>
            {row.lastUsedFrom && <Text fontSize='xs'>{row.lastUsedFrom}</Text>}
          </VStack>
        ),
        { cell: (info) => info.getValue(), header: 'Last used', id: 'lastUsedAt' }
      ),
      columnHelper.accessor((row) => (row.expiresAt && !row.expiresAt.startsWith('0001-') ? fmtDate(row.expiresAt) : 'never'), {
        cell: (info) => info.getValue(),
        header: 'Expires',
        id: 'expiresAt'
      })
    ],
    []
  )

  const archiveColumns = useMemo(
    () => [
      ...columns,
      columnHelper.accessor(
        (row) =>
          row.revoked ? (
            <VStack spacing={0} align='start'>
              <Badge colorScheme='red'>revoked</Badge>
              <Text fontSize='xs'>
                {fmtDate(row.revokedAt)}
                {row.revokedBy ? ` by ${row.revokedBy}` : ''}
              </Text>
            </VStack>
          ) : (
            <Badge colorScheme='orange'>expired</Badge>
          ),
        { cell: (info) => info.getValue(), header: 'State', id: 'state' }
      )
    ],
    [columns]
  )

  const activeColumns = useMemo(
    () => [
      ...columns,
      columnHelper.accessor(
        (row) => (
          <HStack align={'center'} justifyContent={'center'}>
            <RMIconButton
              tooltip={'revoke token'}
              icon={TbTrash}
              onClick={(e) => {
                e.stopPropagation()
                setToRevoke(row)
              }}
            />
          </HStack>
        ),
        { cell: (info) => info.getValue(), header: 'Actions', id: 'actions' }
      )
    ],
    [columns]
  )

  return (
    <>
      <AccordionComponent
        heading={'API TOKENS'}
        allowToggle={false}
        className={styles.accordion}
        panelSX={{ overflowX: 'auto', p: 0 }}
        body={
          <TabItems
            options={[`Active (${active.length})`, `Archive (${archive.length})`]}
            tabIndex={tabIndex}
            onChange={setTabIndex}
            tabContents={[
              <DataTable key='cluster-api-tokens-active' data={active} columns={activeColumns} className={styles.table} />,
              <DataTable key='cluster-api-tokens-archive' data={archive} columns={archiveColumns} className={styles.table} />
            ]}
          />
        }
      />
      {toRevoke && (
        <ConfirmModal
          title={`Revoke API token ${toRevoke.label} of ${toRevoke.user}? Clients using it will be refused immediately.`}
          isOpen={!!toRevoke}
          onConfirmClick={() => {
            dispatch(revokeApiToken({ tokenId: toRevoke.id, clusterName }))
            setToRevoke(null)
          }}
          closeModal={() => setToRevoke(null)}
        />
      )}
    </>
  )
}

export default ClusterApiTokens
