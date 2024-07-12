import React, { useEffect } from 'react'
import PageContainer from './PageContainer'
import TabItems from '../components/TabItems'
import { Box } from '@chakra-ui/react'
import BackLink from '../components/BackLink'
import { useDispatch, useSelector } from 'react-redux'
import Dashboard from './Dashboard'
import { getClusterAlerts, getClusterData, getClusterMaster, setRefreshInterval } from '../redux/clusterSlice'
import { useParams, useSearchParams } from 'react-router-dom'

function Cluster(props) {
  const dispatch = useDispatch()
  const queryParams = useParams()
  const clusterName = queryParams?.name

  const {
    common: { theme },
    cluster: { refreshInterval, clusterData }
  } = useSelector((state) => state)

  useEffect(() => {
    callServices()
  }, [])

  useEffect(() => {
    let intervalId = 0
    let interval = localStorage.getItem('refresh_interval')
      ? parseInt(localStorage.getItem('refresh_interval'))
      : refreshInterval || AppSettings.DEFAULT_INTERVAL

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
    }
  }

  return (
    <PageContainer>
      <Box m='4'>
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
          tabContents={[<Dashboard theme={theme} selectedCluster={clusterData} />]}
        />
      </Box>
    </PageContainer>
  )
}

export default Cluster
