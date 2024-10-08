import React, { useEffect, useState } from 'react'
import { useDispatch, useSelector } from 'react-redux'
import { getClusters } from '../../redux/globalClustersSlice'
import { setCluster } from '../../redux/clusterSlice'
import { Box, Flex, HStack, Text, Wrap } from '@chakra-ui/react'
import NotFound from '../../components/NotFound'
import { AiOutlineCluster } from 'react-icons/ai'
import { HiExclamation } from 'react-icons/hi'
import Card from '../../components/Card'
import TableType2 from '../../components/TableType2'
import styles from './styles.module.scss'
import CheckOrCrossIcon from '../../components/Icons/CheckOrCrossIcon'
import CustomIcon from '../../components/Icons/CustomIcon'
import { FaUserPlus } from 'react-icons/fa'
import RMIconButton from '../../components/RMIconButton'
import AddUserModal from '../../components/Modals/AddUserModal'
import { getMeet } from '../../redux/meetSlice'
import authHeader from '../../services/apiHelper'

function ClusterList({ onClick }) {
  const dispatch = useDispatch()
  const [isAddUserModalOpen, setIsAddUserModalOpen] = useState(false)
  const [clusterName, setClusterName] = useState('')

  const {
    globalClusters: { clusters, loading, monitor }
  } = useSelector((state) => state)

  useEffect(() => {
    dispatch(getClusters({}))
    // getChannels()
  }, [])

  // const getChannels = async () => {
  //   const response = await fetch(`https://repman.marie-dev.svc.cloud18:10005/meet/channels`, {
  //     method: 'GET',
  //     headers: authHeader()
  //   })
  //   console.log('response::', response)
  //   const data = await response.json()
  //   console.log('channels::', data)
  // }

  const openAddUserModal = (e, name) => {
    e.stopPropagation()
    setIsAddUserModalOpen(true)
    setClusterName(name)
  }

  const closeAddUserModal = () => {
    setIsAddUserModalOpen(false)
    setClusterName('')
  }

  return !loading && clusters?.length === 0 ? (
    <NotFound text={'No cluster found!'} />
  ) : (
    <Flex className={styles.clusterList}>
      {clusters?.map((clusterItem, index) => {
        const headerText = clusterItem.name
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
          { key: 'Topology', value: clusterItem.topology },
          { key: 'Orchestrator', value: clusterItem.config?.provOrchestrator },
          { key: 'Databases', value: clusterItem.dbServers?.length },
          { key: 'Proxies', value: clusterItem.proxyServers?.length },
          {
            key: 'Is Healthy',
            value: (
              <HStack spacing='4'>
                {clusterItem.isDown ? (
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
                  as='button'
                  className={styles.btnHeading}
                  onClick={() => {
                    dispatch(setCluster({ data: clusterItem }))
                    if (onClick) {
                      onClick(clusterItem)
                    }
                  }}>
                  <CustomIcon icon={AiOutlineCluster} />
                  <span className={styles.cardHeaderText}>{headerText}</span>
                  {monitor?.config?.monitoringSaveConfig && monitor?.config?.cloud18GitUser?.length > 0 && (
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
  )
}

export default ClusterList
