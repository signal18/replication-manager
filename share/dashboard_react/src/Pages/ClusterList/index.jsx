import React, { useEffect, useMemo, useState } from 'react'
import { useDispatch, useSelector } from 'react-redux'
import { getClusters } from '../../redux/globalClustersSlice'
import { setCluster } from '../../redux/clusterSlice'
import { Box, Flex, HStack, Text, Wrap } from '@chakra-ui/react'
import NotFound from '../../components/NotFound'
import { AiOutlineCluster } from 'react-icons/ai'
import { HiCreditCard, HiExclamation, HiTable, HiViewGrid } from 'react-icons/hi'
import Card from '../../components/Card'
import TableType2 from '../../components/TableType2'
import { DataTable } from '../../components/DataTable'
import { createColumnHelper } from '@tanstack/react-table'
import styles from './styles.module.scss'
import CheckOrCrossIcon from '../../components/Icons/CheckOrCrossIcon'
import CustomIcon from '../../components/Icons/CustomIcon'
import { FaUserPlus } from 'react-icons/fa'
import RMIconButton from '../../components/RMIconButton'
import TagPill from '../../components/TagPill'
import AddUserModal from '../../components/Modals/AddUserModal'
import SearchBox from '../../components/SearchBox'
import AccordionComponent from '../../components/AccordionComponent'

const columnHelper = createColumnHelper()

function ClusterList({ onClick }) {
  const dispatch = useDispatch()
  const [isAddUserModalOpen, setIsAddUserModalOpen] = useState(false)
  const [clusterName, setClusterName] = useState('')
  const [addUserApiUsers, setAddUserApiUsers] = useState(null)
  const [clusterList, setClusterList] = useState([])
  const [search, setSearch] = useState("")
  const [viewType, setViewType] = useState('table')

  const clusters = useSelector((state) => state.globalClusters.clusters)
  const loading = useSelector((state) => state.globalClusters.loading)
  const monitor = useSelector((state) => state.globalClusters.monitor)
  const isDownList = useSelector((state) => state.globalClusters.isDownList)
  const isFailableList = useSelector((state) => state.globalClusters.isFailableList)
  const isAdmin = localStorage.getItem('username') === 'admin'

  const canAddUser = monitor?.config?.monitoringSaveConfig && monitor?.config?.cloud18GitUser?.length > 0 && isAdmin

  useEffect(() => {
    dispatch(getClusters({}))
    // getChannels()
  }, [])

  useEffect(() => {
    if (search === "") {
      setClusterList(clusters || [])
    } else {
      setClusterList(clusters?.filter((cluster) => cluster?.name.toLowerCase().includes(search.toLowerCase())) || [])
    }
  }, [isDownList, isFailableList, clusters, search])

  const selectCluster = (clusterItem) => {
    dispatch(setCluster({ data: clusterItem }))
    if (onClick) {
      onClick(clusterItem)
    }
  }

  const openAddUserModal = (e, clusterItem) => {
    e.stopPropagation()
    setClusterName(clusterItem.name)
    setAddUserApiUsers(clusterItem.apiUsers || {})
    setIsAddUserModalOpen(true)
  }

  const closeAddUserModal = () => {
    setIsAddUserModalOpen(false)
    setClusterName('')
    setAddUserApiUsers(null)
  }

  const arbitrationPill = (clusterItem) =>
    clusterItem.activePassiveStatus === 'A' ? (
      <TagPill colorScheme='green' text='Active' />
    ) : clusterItem.activePassiveStatus === 'S' ? (
      <TagPill colorScheme='orange' text='Standby' isBlinking={true} />
    ) : (
      <Text>-</Text>
    )

  const healthCell = (clusterItem) =>
    clusterItem.isDown || clusterItem.isMasterDown ? (
      <HStack spacing='2'>
        <CheckOrCrossIcon isValid={false} />
        <Text>No</Text>
      </HStack>
    ) : !clusterItem.isFailable ? (
      <HStack spacing='2'>
        <CustomIcon icon={HiExclamation} color='red' />
        <Text>Blocker</Text>
      </HStack>
    ) : (
      <HStack spacing='2'>
        <CheckOrCrossIcon isValid={true} />
        <Text>Yes</Text>
      </HStack>
    )

  const hasArbitration = clusterList?.some((c) => c.config?.arbitrationExternal)

  const columns = useMemo(
    () => [
      columnHelper.accessor(
        (row) => {
          const isPending = Object.entries(row?.apiUsers || {}).filter(([_, u]) => u.roles.pending).length > 0
          const isSponsor = Object.entries(row?.apiUsers || {}).filter(([_, u]) => u.roles.sponsor).length > 0
          return (
            <HStack cursor='pointer' onClick={() => selectCluster(row)}>
              <CustomIcon
                icon={isSponsor || isPending ? HiCreditCard : AiOutlineCluster}
                fill={isSponsor ? 'green' : isPending ? 'orange' : 'gray'}
              />
              <Text className={styles.cardHeaderText}>{row.name}</Text>
            </HStack>
          )
        },
        { cell: (info) => info.getValue(), header: 'Cluster', id: 'name' }
      ),
      ...(hasArbitration
        ? [
            columnHelper.accessor((row) => (row.config?.arbitrationExternal ? arbitrationPill(row) : <Text>-</Text>), {
              cell: (info) => info.getValue(),
              header: 'Arbitration',
              id: 'arbitration'
            })
          ]
        : []),
      columnHelper.accessor((row) => <CheckOrCrossIcon isValid={!row.config?.monitoringPause} />, {
        cell: (info) => info.getValue(),
        header: 'Monitoring',
        id: 'monitoring'
      }),
      columnHelper.accessor((row) => row.topology, {
        cell: (info) => info.getValue(),
        header: 'Topology',
        id: 'topology'
      }),
      columnHelper.accessor((row) => row.config?.provOrchestrator, {
        cell: (info) => info.getValue(),
        header: 'Orchestrator',
        id: 'orchestrator'
      }),
      columnHelper.accessor((row) => row.dbServers?.length, {
        cell: (info) => info.getValue(),
        header: 'Databases',
        id: 'databases'
      }),
      columnHelper.accessor((row) => row.proxyServers?.length, {
        cell: (info) => info.getValue(),
        header: 'Proxies',
        id: 'proxies'
      }),
      columnHelper.accessor((row) => healthCell(row), {
        cell: (info) => info.getValue(),
        header: 'Healthy',
        id: 'healthy'
      }),
      columnHelper.accessor((row) => <CheckOrCrossIcon isValid={!!row.isProvision} />, {
        cell: (info) => info.getValue(),
        header: 'Provisioned',
        id: 'provisioned'
      }),
      columnHelper.accessor((row) => row.uptime, {
        cell: (info) => info.getValue(),
        header: 'SLA',
        id: 'sla'
      }),
      ...(canAddUser
        ? [
            columnHelper.accessor(
              (row) => (
                <RMIconButton
                  icon={FaUserPlus}
                  tooltip={'Add User'}
                  px='2'
                  variant='outline'
                  onClick={(e) => openAddUserModal(e, row)}
                />
              ),
              { cell: (info) => info.getValue(), header: '', id: 'options' }
            )
          ]
        : [])
    ],
    [hasArbitration, canAddUser, clusterList]
  )

  return !loading && clusterList?.length === 0 ? (
    <NotFound text={'No cluster found!'} />
  ) : (
    <>
      <AccordionComponent
        heading={'Clusters'}
        headerActions={
          <HStack spacing='2'>
            <SearchBox className={styles.searchBox} value={search} size='sm' placeholder='Search' onChange={setSearch} />
            {viewType === 'table' ? (
              <RMIconButton icon={HiViewGrid} tooltip='Show grid view' onClick={() => setViewType('grid')} />
            ) : (
              <RMIconButton icon={HiTable} tooltip='Show table view' onClick={() => setViewType('table')} />
            )}
          </HStack>
        }
        body={viewType === 'table' ? (
          <DataTable data={clusterList} columns={columns} />
        ) : (
        <Flex className={styles.clusterList}>
          {clusterList?.map((clusterItem, index) => {
            const headerText = clusterItem.name
            const isPending = Object.entries(clusterItem?.apiUsers).filter(([_, u]) => u.roles.pending).length > 0
            const isSponsor = Object.entries(clusterItem?.apiUsers).filter(([_, u]) => u.roles.sponsor).length > 0
            const dataObject = [
              {
                key: 'Is Monitoring',
                value: (
                  <HStack spacing='4'>
                    {clusterItem.config?.monitoringPause ? (
                      <>
                        <CheckOrCrossIcon isValid={false} />
                        <Text>No</Text>
                      </>
                    ) : (
                      <>
                        <CheckOrCrossIcon isValid={true} />
                        <Text>Yes</Text>
                      </>
                    )}
                  </HStack>
                )
              },
              ...(clusterItem.config?.arbitrationExternal
                ? [
                    {
                      key: 'Arbitration',
                      value: arbitrationPill(clusterItem)
                    }
                  ]
                : []),
              { key: 'Topology', value: clusterItem.topology },
              { key: 'Orchestrator', value: clusterItem.config?.provOrchestrator },
              { key: 'Databases', value: clusterItem.dbServers?.length },
              { key: 'Proxies', value: clusterItem.proxyServers?.length },
              {
                key: 'Is Healthy',
                value: healthCell(clusterItem)
              },
              {
                key: 'Is Provisioned',
                value: (
                  <HStack spacing='4'>
                    {clusterItem.isProvision ? (
                      <>
                        <CheckOrCrossIcon isValid={true} />
                        <Text>Yes</Text>
                      </>
                    ) : (
                      <>
                        <CheckOrCrossIcon isValid={false} />
                        <Text>No</Text>
                      </>
                    )}
                  </HStack>
                )
              },
              { key: 'SLA', value: clusterItem.uptime }
            ]

            return (
              <Box key={clusterItem.name} className={styles.cardWrapper}>
                <Card
                  className={`${styles.card}`}
                  width={'400px'}
                  header={
                    <HStack
                      as='div'
                      cursor={'pointer'}
                      className={styles.btnHeading}
                      onClick={() => selectCluster(clusterItem)}>
                      <CustomIcon icon={isSponsor || isPending ? (HiCreditCard) : (AiOutlineCluster)} fill={isSponsor ? "green" : isPending ? "orange" : "gray"} />
                      <span className={styles.cardHeaderText}>{headerText}</span>
                      {canAddUser && (
                        <RMIconButton
                          icon={FaUserPlus}
                          tooltip={'Add User'}
                          px='2'
                          variant='outline'
                          onClick={(e) => openAddUserModal(e, clusterItem)}
                          className={styles.btnAddUser}
                        />
                      )}
                    </HStack>
                  }
                  body={
                    <TableType2
                      dataArray={dataObject}
                      className={styles.table}
                      labelClassName={styles.rowLabel}
                      valueClassName={styles.rowValue}
                    />
                  }
                />
              </Box>
            )
          })}
        </Flex>
        )}
      />
      {isAddUserModalOpen && (
        <AddUserModal clusterName={clusterName} apiUsers={addUserApiUsers} isOpen={isAddUserModalOpen} closeModal={closeAddUserModal} />
      )}
    </>
  )
}

export default ClusterList
