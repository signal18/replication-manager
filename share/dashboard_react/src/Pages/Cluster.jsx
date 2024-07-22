import React, { useEffect } from 'react'
import PageContainer from './PageContainer'
import TabItems from '../components/TabItems'
import { Box } from '@chakra-ui/react'
import { useDispatch, useSelector } from 'react-redux'
import Dashboard from './Dashboard'
import { getClusterAlerts, getClusterData, getClusterMaster, getClusterServers } from '../redux/clusterSlice'
import { useParams } from 'react-router-dom'

function Cluster(props) {
  const dispatch = useDispatch()
  const queryParams = useParams()
  const clusterName = queryParams?.name

  const {
    cluster: { refreshInterval, clusterData }
  } = useSelector((state) => state)

  useEffect(() => {
    callServices()
  }, [])

  useEffect(() => {
    let interval = localStorage.getItem('refresh_interval')
      ? parseInt(localStorage.getItem('refresh_interval'))
      : refreshInterval || AppSettings.DEFAULT_INTERVAL
    let intervalId = 0
    if (interval > 0) {
      callServices()
      intervalId = setInterval(() => {
        callServices()
      }, interval * 1000)
    }
    return () => {
      clearInterval(intervalId)
    }
  }, [refreshInterval])

  const callServices = () => {
    if (clusterName) {
      dispatch(getClusterData({ clusterName }))
      dispatch(getClusterAlerts({ clusterName }))
      dispatch(getClusterMaster({ clusterName }))
      dispatch(getClusterServers({ clusterName }))
    }
  }

  return (
    <PageContainer>
      <Box m='4'>
        <TabItems
          options={['Dashboard', 'Proxies', 'Settings', 'Configs', 'Agents', 'Certificates', 'Queryrules', 'Shards']}
          tabContents={[<Dashboard selectedCluster={clusterData} />]}
        />
      </Box>
    </PageContainer>
  )
}

export default Cluster
