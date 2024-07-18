import React, { useEffect } from 'react'
import { useDispatch, useSelector } from 'react-redux'
import { getClusters, getMonitoredData, setRefreshInterval } from '../redux/clusterSlice'
import { HStack, Icon, Link, Text, useColorMode, Wrap } from '@chakra-ui/react'
import NotFound from '../components/NotFound'
import { AiOutlineCluster } from 'react-icons/ai'
import { HiCheck, HiExclamation, HiX } from 'react-icons/hi'
import { Link as ReactRouterLink } from 'react-router-dom'
import Card from '../components/Card'
import { AppSettings } from '../AppSettings'
import TableType2 from '../components/TableType2'

function ClusterList(props) {
  const dispatch = useDispatch()
  const { colorMode } = useColorMode()

  const {
    cluster: { clusters, loading, refreshInterval }
  } = useSelector((state) => state)
  useEffect(() => {
    dispatch(getClusters({}))
  }, [])

  useEffect(() => {
    let interval = localStorage.getItem('refresh_interval')
      ? parseInt(localStorage.getItem('refresh_interval'))
      : AppSettings.DEFAULT_INTERVAL

    dispatch(setRefreshInterval({ interval }))
    let intervalId = 0

    if (refreshInterval > 0) {
      callServices()
      const intervalSeconds = refreshInterval * 1000
      intervalId = setInterval(() => {
        callServices()
      }, intervalSeconds)
    }
    return () => {
      clearInterval(intervalId)
    }
  }, [refreshInterval])

  const callServices = () => {
    dispatch(getClusters({}))
    dispatch(getMonitoredData({}))
  }

  const styles = {
    linkCard: {
      _hover: {
        color: 'inherit'
      }
    },
    icon: {
      fontSize: '1.5rem'
    },
    green: {
      fill: colorMode === 'light' ? 'green' : 'lightgreen'
    },
    red: { fill: 'red' },

    orange: {
      fill: 'orange'
    }
  }

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
                    <Icon css={{ ...styles.icon, ...styles.red }} as={HiX} />
                    <Text>No</Text>
                  </>
                ) : (
                  <>
                    <Icon css={{ ...styles.icon, ...styles.green }} as={HiCheck} />
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
                    <Icon sx={{ ...styles.icon, ...styles.red }} as={HiX} />
                    <Text>No</Text>
                  </>
                ) : !clusterItem.isFailable ? (
                  <>
                    <Icon sx={{ ...styles.icon, ...styles.orange }} as={HiExclamation} />
                    <Text>Warning</Text>
                  </>
                ) : (
                  <>
                    <Icon sx={{ ...styles.icon, ...styles.green }} as={HiCheck} />
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
                    <Icon css={[styles.icon, styles.green]} as={HiCheck} />
                    <Text>Yes</Text>
                  </>
                ) : (
                  <>
                    <Icon css={[styles.icon, styles.red]} as={HiX} />
                    <Text>No</Text>
                  </>
                )}
              </HStack>
            )
          },
          { key: 'SLA', value: clusterItem.uptime }
        ]
        return (
          <Link sx={styles.linkCard} as={ReactRouterLink} mt='8' to={`/clusters/${clusterItem.name}`}>
            <Card
              width={'400px'}
              header={
                <HStack size={'sm'} sx={styles.heading}>
                  <Icon fontSize='1.5rem' as={AiOutlineCluster} /> <span>{clusterItem.name}</span>
                </HStack>
              }
              body={<TableType2 dataArray={dataObject} />}
            />
          </Link>
        )
      })}
    </Wrap>
  )
}

export default ClusterList
