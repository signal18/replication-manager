import React, { useEffect } from 'react'
import PageContainer from './PageContainer'
import TabItems from '../components/TabItems'
import { Box } from '@chakra-ui/react'
import BackLink from '../components/BackLink'
import { useDispatch, useSelector } from 'react-redux'
import Dashboard from './Dashboard'
import { getClusterAlerts, getClusterData, getClusterMaster } from '../redux/clusterSlice'

function Cluster(props) {
  const dispatch = useDispatch()
  const {
    common: { theme },
    cluster: { selectedCluster, refreshInterval }
  } = useSelector((state) => state)

  useEffect(() => {
    let intervalId = 0
    if (refreshInterval > 0) {
      callServices()
      intervalId = setInterval(() => {
        callServices()
      }, refreshInterval * 1000)
    }
    return () => {
      clearInterval(intervalId)
    }
  }, [refreshInterval])

  const callServices = () => {
    if (selectedCluster) {
      dispatch(getClusterData({ clusterName: selectedCluster.name }))
      dispatch(getClusterAlerts({ clusterName: selectedCluster.name }))
      dispatch(getClusterMaster({ clusterName: selectedCluster.name }))
    }
  }

  return (
    <PageContainer>
      <Box m='4'>
        <BackLink path={`/clusters`} mb='2' />
        <TabItems
          options={[
            'Dashboard',
            'Alerts',
            'Proxies',
            'Settings',
            'Configs',
            'Agents',
            'Certificates',
            'Queryrules',
            'Shards'
          ]}
          tabContents={[<Dashboard selectedCluster={selectedCluster} theme={theme} />]}
        />
      </Box>
    </PageContainer>
  )
}

export default Cluster
