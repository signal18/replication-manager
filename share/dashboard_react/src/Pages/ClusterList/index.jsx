import React from 'react'
import { useDispatch, useSelector } from 'react-redux'
import { setCluster } from '../../redux/clusterSlice'
import { Box, HStack, Text, Wrap } from '@chakra-ui/react'
import NotFound from '../../components/NotFound'
import { AiOutlineCluster } from 'react-icons/ai'
import { HiExclamation } from 'react-icons/hi'
import Card from '../../components/Card'
import TableType2 from '../../components/TableType2'
import styles from './styles.module.scss'
import CheckOrCrossIcon from '../../components/Icons/CheckOrCrossIcon'
import CustomIcon from '../../components/Icons/CustomIcon'

function ClusterList({ onClick }) {
  const dispatch = useDispatch()

  const {
    cluster: { clusters, loading }
  } = useSelector((state) => state)

  return !loading && clusters?.length === 0 ? (
    <NotFound text={'No cluster found!'} />
  ) : (
    <Wrap>
      {clusters?.map((clusterItem) => {
        const dataObject = [
          {
            key: 'Is Monitoring',
            value: (
              <HStack spacing='4'>
                {clusterItem.config.monitoringPause ? (
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
          { key: 'Orchestrator', value: clusterItem.config.provOrchestrator },
          { key: 'Databases', value: clusterItem.dbServers.length },
          { key: 'Proxies', value: clusterItem.proxyServers.length },
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
          <Box
            className={styles.cardWrapper}
            as={'button'}
            onClick={() => {
              dispatch(setCluster({ data: clusterItem }))
              if (onClick) {
                onClick(clusterItem)
              }
            }}>
            <Card
              className={styles.card}
              width={'400px'}
              header={
                <HStack className={styles.heading}>
                  <CustomIcon icon={AiOutlineCluster} />{' '}
                  <span className={styles.cardHeaderText}>{clusterItem.name}</span>
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
    </Wrap>
  )
}

export default ClusterList
