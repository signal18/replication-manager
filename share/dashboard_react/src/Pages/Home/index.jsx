import React, { useEffect, useRef, useState } from 'react'
import { Box } from '@chakra-ui/react'

import PageContainer from '../PageContainer'
import TabItems from '../../components/TabItems'
import ClusterList from '../ClusterList'
import { useDispatch, useSelector } from 'react-redux'
import {
  getBackupSnapshot,
  getClusterAlerts,
  getClusterCertificates,
  getClusterData,
  getClusterMaster,
  getClusterProxies,
  getClusters,
  getClusterServers,
  getMonitoredData,
  getQueryRules,
  getShardSchema,
  getTopProcess,
  setCluster,
  setRefreshInterval
} from '../../redux/clusterSlice'
import { AppSettings } from '../../AppSettings'
import styles from './styles.module.scss'
import { useParams } from 'react-router-dom'
import { HiArrowNarrowLeft } from 'react-icons/hi'
import CustomIcon from '../../components/Icons/CustomIcon'
import Dashboard from '../Dashboard'
import Settings from '../Settings'
import Configs from '../Configs'
import Graphs from '../Graphs'
import Agents from '../Agents'
import Backups from '../Backups'
import Top from '../Top'
import Shards from '../Shards'
import QueryRules from '../QueryRules'

function Home() {
  const dispatch = useDispatch()
  const selectedTabRef = useRef(0)
  const selectedClusterNameRef = useRef('')
  const [selectedTab, setSelectedTab] = useState(0)
  const [user, setUser] = useState(null)
  const [selectedCluster, setSelectedCluster] = useState(null)
  const dashboardTabsRef = useRef([])

  const params = useParams()

  const {
    cluster: { refreshInterval, clusterData }
  } = useSelector((state) => state)

  useEffect(() => {
    if (params?.cluster) {
      setDashboardTab({ name: params.cluster })
    }
  }, [])

  useEffect(() => {
    if (clusterData) {
      setSelectedCluster(clusterData)
      if (clusterData.apiUsers) {
        const loggedUser = localStorage.getItem('username')
        if (loggedUser && clusterData?.apiUsers[loggedUser]) {
          const apiUser = clusterData.apiUsers[loggedUser]
          setUser(apiUser)
          const authorizedTabs = ['Dashboard', 'Settings', 'Configs']
          if (clusterData.config.graphiteMetrics && apiUser.grants['cluster-show-graphs']) {
            authorizedTabs.push('Graphs')
          }
          if (apiUser.grants['cluster-show-agents']) {
            authorizedTabs.push('Agents')
          }
          if (apiUser.grants['cluster-show-backups']) {
            authorizedTabs.push('Backups')
          }
          if (apiUser.grants['db-show-process']) {
            authorizedTabs.push('Tops')
          }
          if (clusterData.config.proxysql && apiUser.grants['cluster-show-agents']) {
            authorizedTabs.push('Query Rules')
          }
          if (apiUser.grants['db-show-schema']) {
            authorizedTabs.push('Shards')
          }
          dashboardTabsRef.current = authorizedTabs
        }
      }
    }
  }, [clusterData])

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

  const renderClusterListTabWithArrow = () => {
    return (
      <>
        <CustomIcon icon={HiArrowNarrowLeft} /> Clusters
      </>
    )
  }

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
      if (dashboardTabsRef.current[selectedTabRef.current - 1] === 'Configs') {
        dispatch(getClusterCertificates({ clusterName: selectedClusterNameRef.current }))
      }
      if (dashboardTabsRef.current[selectedTabRef.current - 1] === 'Backups') {
        dispatch(getBackupSnapshot({ clusterName: selectedClusterNameRef.current }))
      }
      if (dashboardTabsRef.current[selectedTabRef.current - 1] === 'Tops') {
        dispatch(getTopProcess({ clusterName: selectedClusterNameRef.current }))
      }
      if (dashboardTabsRef.current[selectedTabRef.current - 1] === 'Query Rules') {
        dispatch(getQueryRules({ clusterName: selectedClusterNameRef.current }))
      }
      if (dashboardTabsRef.current[selectedTabRef.current - 1] === 'Shards') {
        dispatch(getShardSchema({ clusterName: selectedClusterNameRef.current }))
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
          options={selectedTab > 0 ? [renderClusterListTabWithArrow(), ...dashboardTabsRef.current] : ['Clusters']}
          tabContents={[
            <ClusterList onClick={setDashboardTab} />,
            <Dashboard user={user} selectedCluster={selectedCluster} />,
            <Settings user={user} selectedCluster={selectedCluster} />,
            <Configs user={user} selectedCluster={selectedCluster} />,
            ...(selectedCluster?.config?.graphiteMetrics && user?.grants['cluster-show-graphs'] ? [<Graphs />] : []),
            ...(user?.grants['cluster-show-agents'] ? [<Agents user={user} selectedCluster={selectedCluster} />] : []),
            ...(user?.grants['cluster-show-backups'] ? [<Backups selectedCluster={selectedCluster} />] : []),
            ...(user?.grants['db-show-process'] ? [<Top selectedCluster={selectedCluster} />] : []),
            ...(selectedCluster?.config?.proxysql && user?.grants['cluster-show-agents']
              ? [<QueryRules selectedCluster={selectedCluster} />]
              : []),
            ...(user?.grants['db-show-schema'] ? [<Shards selectedCluster={selectedCluster} />] : [])
          ]}
        />
      </Box>
    </PageContainer>
  )
}

export default Home
