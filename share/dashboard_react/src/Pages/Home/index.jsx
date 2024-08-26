import React, { useEffect, useRef, useState } from 'react'
import { Box } from '@chakra-ui/react'

import PageContainer from '../PageContainer'
import TabItems from '../../components/TabItems'
import ClusterList from '../ClusterList'
import { useDispatch, useSelector } from 'react-redux'
import {
  getClusterAlerts,
  getClusterCertificates,
  getClusterData,
  getClusterMaster,
  getClusterProxies,
  getClusters,
  getClusterServers,
  getMonitoredData,
  setCluster,
  setRefreshInterval
} from '../../redux/clusterSlice'
import Cluster from '../Cluster'
import { AppSettings } from '../../AppSettings'
import styles from './styles.module.scss'

function Home() {
  const dispatch = useDispatch()
  const selectedTabRef = useRef(0)
  const selectedClusterNameRef = useRef('')
  const [selectedTab, setSelectedTab] = useState(0)

  const {
    cluster: { refreshInterval }
  } = useSelector((state) => state)

  useEffect(() => {
    let intervalId = 0
    let interval = localStorage.getItem('refresh_interval')
      ? parseInt(localStorage.getItem('refresh_interval'))
      : AppSettings.DEFAULT_INTERVAL

    dispatch(setRefreshInterval({ interval }))

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

  const dashboardTabs = ['Dashboard', 'Settings', 'Configs', 'Agents', 'Queryrules', 'Shards']

  const callServices = () => {
    if (selectedTabRef.current === 0) {
      dispatch(getClusters({}))
      dispatch(getMonitoredData({}))
    } else if (selectedClusterNameRef.current) {
      const isAutoReloadPaused = localStorage.getItem('pause_auto_reload')
      if (!isAutoReloadPaused) {
        dispatch(getClusterData({ clusterName: selectedClusterNameRef.current }))
        dispatch(getClusterAlerts({ clusterName: selectedClusterNameRef.current }))
        dispatch(getClusterMaster({ clusterName: selectedClusterNameRef.current }))
        dispatch(getClusterServers({ clusterName: selectedClusterNameRef.current }))
        dispatch(getClusterProxies({ clusterName: selectedClusterNameRef.current }))
      }
      if (selectedTabRef.current === 3) {
        dispatch(getClusterCertificates({ clusterName: selectedClusterNameRef.current }))
      }
    }
  }
  const handleTabChange = (tabIndex) => {
    selectedTabRef.current = tabIndex
    setSelectedTab(tabIndex)
    if (tabIndex === 0) {
      dispatch(setCluster({ data: null }))
      selectedClusterNameRef.current = ''
    }
  }

  const setDashboardTab = (cluster) => {
    selectedTabRef.current = 1
    selectedClusterNameRef.current = cluster.name
    setSelectedTab(1)
  }

  return (
    <PageContainer>
      <Box className={styles.container}>
        <TabItems
          tabIndex={selectedTab}
          onChange={handleTabChange}
          options={selectedTab > 0 ? ['Clusters', ...dashboardTabs] : ['Clusters']}
          tabContents={[
            <ClusterList onClick={setDashboardTab} />,
            <Cluster tab='dashboard' />,
            <Cluster tab='settings' />,
            <Cluster tab='configs' />,
            <Cluster tab='agents' />
          ]}
        />
      </Box>
    </PageContainer>
  )
}

export default Home
