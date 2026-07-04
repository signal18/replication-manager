import React, { useEffect, useState } from 'react'
import { useDispatch, useSelector } from 'react-redux'
import { getClusters } from '../../redux/globalClustersSlice'
import { getClusterData, setCluster } from '../../redux/clusterSlice'
import { Box, Flex, HStack, Text, Wrap } from '@chakra-ui/react'
import NotFound from '../../components/NotFound'
import { AiOutlineCluster } from 'react-icons/ai'
import { HiCreditCard, HiExclamation } from 'react-icons/hi'
import Card from '../../components/Card'
import TableType2 from '../../components/TableType2'
import styles from './styles.module.scss'
import CheckOrCrossIcon from '../../components/Icons/CheckOrCrossIcon'
import CustomIcon from '../../components/Icons/CustomIcon'
import { FaUserPlus } from 'react-icons/fa'
import RMIconButton from '../../components/RMIconButton'
import TagPill from '../../components/TagPill'
import AddUserModal from '../../components/Modals/AddUserModal'
import SearchBox from '../../components/SearchBox'

function ClusterList({ onClick }) {
  const dispatch = useDispatch()
  const [isAddUserModalOpen, setIsAddUserModalOpen] = useState(false)
  const [clusterName, setClusterName] = useState('')
  const [clusterList, setClusterList] = useState([])
  const [search, setSearch] = useState("")

  const clusters = useSelector((state) => state.globalClusters.clusters)
  const loading = useSelector((state) => state.globalClusters.loading)
  const monitor = useSelector((state) => state.globalClusters.monitor)
  const isDownList = useSelector((state) => state.globalClusters.isDownList)
  const isFailableList = useSelector((state) => state.globalClusters.isFailableList)
  const isAdmin = localStorage.getItem('username') === 'admin'

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

  const openAddUserModal = (e, name) => {
    e.stopPropagation()
    setClusterName(name)
    dispatch(getClusterData({ clusterName: name }))
    setIsAddUserModalOpen(true)
  }

  const closeAddUserModal = () => {
    setIsAddUserModalOpen(false)
    setClusterName('')
  }

  return !loading && clusterList?.length === 0 ? (
    <NotFound text={'No cluster found!'} />
  ) : (
    <>
      <Flex className={styles.searchWrapper}>
        <SearchBox className={styles.searchBox} value={search} size='md' placeholder='Search' onChange={setSearch} />
      </Flex>
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
                    value:
                      clusterItem.activePassiveStatus === 'A' ? (
                        <TagPill colorScheme='green' text='Active' />
                      ) : clusterItem.activePassiveStatus === 'S' ? (
                        <TagPill colorScheme='orange' text='Standby' isBlinking={true} />
                      ) : (
                        <Text>-</Text>
                      )
                  }
                ]
              : []),
            { key: 'Topology', value: clusterItem.topology },
            { key: 'Orchestrator', value: clusterItem.config?.provOrchestrator },
            { key: 'Databases', value: clusterItem.dbServers?.length },
            { key: 'Proxies', value: clusterItem.proxyServers?.length },
            {
              key: 'Is Healthy',
              value: (
                <HStack spacing='4'>
                  {clusterItem.isDown || clusterItem.isMasterDown ? (
                    <>
                      <CheckOrCrossIcon isValid={false} />
                      <Text>No</Text>
                    </>
                  ) : !clusterItem.isFailable ? (
                    <>
                      <CustomIcon icon={HiExclamation} color='orange' />
                      <Text>Warning</Text>
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
                    onClick={() => {
                      dispatch(setCluster({ data: clusterItem }))
                      if (onClick) {
                        onClick(clusterItem)
                      }
                    }}>
                    <CustomIcon icon={isSponsor || isPending ? (HiCreditCard) : (AiOutlineCluster)} fill={isSponsor ? "green" : isPending ? "orange" : "gray"} />
                    <span className={styles.cardHeaderText}>{headerText}</span>
                    {monitor?.config?.monitoringSaveConfig && monitor?.config?.cloud18GitUser?.length > 0 && isAdmin && (
                      <RMIconButton
                        icon={FaUserPlus}
                        tooltip={'Add User'}
                        px='2'
                        variant='outline'
                        onClick={(e) => openAddUserModal(e, clusterItem.name)}
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
        {isAddUserModalOpen && (
          <AddUserModal clusterName={clusterName} isOpen={isAddUserModalOpen} closeModal={closeAddUserModal} />
        )}
      </Flex>
    </>
  )
}

export default ClusterList
