import React, { useEffect, useRef, useState } from 'react'
import { Box } from '@chakra-ui/react'

import PageContainer from '../PageContainer'
import TabItems from '../../components/TabItems'
import ClusterList from '../ClusterList'
import { useDispatch, useSelector } from 'react-redux'
import {
  getResticSnapshot,
  getClusterAlerts,
  getClusterCertificates,
  getClusterData,
  getClusterMaster,
  getClusterProxies,
  getClusterServers,
  getJobs,
  getQueryRules,
  getShardSchema,
  getTopProcess,
  setCluster,
  setRefreshInterval,
  pauseAutoReload,
  getBackupStats,
  clearCluster,
  getClusterApps,
  getOpenSVCStats,
  getClusterLogs,
  getBackups,
  getResticCurrentTask
} from '../../redux/clusterSlice'
import { getClusters, getMonitoredData, getClusterPeers, getClusterForSale, getGlobalAlerts, getGlobalMetrics, getGlobalLogs } from '../../redux/globalClustersSlice'
import { AppSettings } from '../../AppSettings'
import styles from './styles.module.scss'
import { useHref, useParams } from 'react-router-dom'
import { HiArrowNarrowLeft } from 'react-icons/hi'
import CustomIcon from '../../components/Icons/CustomIcon'
import Dashboard from '../Dashboard'
import Settings from '../Settings'
import Configs from '../Configs'
import Graphs from '../Graphs'
import Agents from '../Agents'
import Users from '../Users'
import Maintenance from '../Maintenance'
import Top from '../Top'
import Shards from '../Shards'
import QueryRules from '../QueryRules'
import PeerClusterList from '../PeerClusterList'
import ClustersGlobalSettings from '../ClustersGlobalSettings'
import GlobalItems from '../GlobalItems'
import Billing from '../Billing'
import NewClusterModal from '../../components/Modals/NewClusterModal'
import { FaPlus } from 'react-icons/fa'
import { setBaseURL } from '../../redux/authSlice'


function Home() {
  const dispatch = useDispatch()
  const selectedTabRef = useRef(0)
  const selectedClusterNameRef = useRef('')
  const isClusterOpenRef = useRef(false)
  const [selectedTab, setSelectedTab] = useState(0)
  const [user, setUser] = useState(null)
  const [selectedCluster, setSelectedCluster] = useState(null)
  const dashboardTabsRef = useRef([])
  const globalTabsRef = useRef([])
  const intervalTickerRef = useRef(0)
  const [isNewClusterModalOpen, setIsNewClusterModalOpen] = useState(false)
  const params = useParams()

  const terminalURL = useHref('/terminal/')

  const loggedUser = useSelector((state) => state.auth.user)
  const refreshInterval = useSelector((state) => state.cluster.refreshInterval)
  const clusterData = useSelector((state) => state.cluster.clusterData)
  const monitor = useSelector((state) => state.globalClusters.monitor)
  const clusters = useSelector((state) => state.globalClusters.clusters)
  const clusterPeers = useSelector((state) => state.globalClusters.clusterPeers)
  const clustersLoading = useSelector((state) => state.globalClusters.loading)

  const isSSOUser = !!loggedUser?.profile

  useEffect(() => {
    if (params?.cluster) {
      setDashboardTab({ name: params.cluster })
    }
  }, [])

  useEffect(() => {
    if (monitor?.config?.cloud18) {
      globalTabsRef.current = ['Clusters Local', 'Clusters Peer', 'Clusters For Sale']
      if (isSSOUser) {
        globalTabsRef.current.push('Billing')
      }

    } else {
      globalTabsRef.current = ['Clusters Local']
    }
    if (loggedUser?.User == "admin") {
      globalTabsRef.current.push('Settings')
    }
    globalTabsRef.current.push('Dashboard')
  }, [monitor?.config?.cloud18, loggedUser?.User, isSSOUser])

  // For SSO users: if local clusters are empty after loading, advance to Peer,
  // then to For Sale if Peer is also empty.
  useEffect(() => {
    if (
      isClusterOpenRef.current ||     // a cluster is already open — leave it alone
      !isSSOUser ||                   // non-SSO users stay on local
      !monitor?.config?.cloud18 ||    // peer/for-sale tabs only exist when cloud18 is on
      clustersLoading                 // wait until getClusters has finished
    ) return

    const currentTab = globalTabsRef.current[selectedTabRef.current]

    if (currentTab === 'Clusters Local' && clusters.length === 0) {
      // Local empty → try Peer
      const peerIdx = globalTabsRef.current.indexOf('Clusters Peer')
      if (peerIdx >= 0) {
        selectedTabRef.current = peerIdx
        setSelectedTab(peerIdx)
        dispatch(getClusterPeers({}))
      }
    } else if (currentTab === 'Clusters Peer' && clusterPeers !== null && clusterPeers.length === 0) {
      // Peer empty → try For Sale
      const forSaleIdx = globalTabsRef.current.indexOf('Clusters For Sale')
      if (forSaleIdx >= 0) {
        selectedTabRef.current = forSaleIdx
        setSelectedTab(forSaleIdx)
        dispatch(getClusterForSale({}))
      }
    }
  }, [clusters, clusterPeers, clustersLoading, isSSOUser, monitor?.config?.cloud18])

  useEffect(() => {
    if (clusterData) {
      setSelectedCluster(clusterData)
      if (clusterData.apiUsers && loggedUser) {
          const apiUser = clusterData.apiUsers[loggedUser.User] ? clusterData.apiUsers[loggedUser.User] : clusterData.apiUsers[loggedUser.Email]
          if (apiUser) {
            setUser(apiUser)
            const authorizedTabs = [selectedClusterNameRef.current, 'Settings', 'Configs']
            if (clusterData.config.graphiteMetrics && apiUser.grants['cluster-show-graphs']) {
              authorizedTabs.push('Graphs')
            }
            if (apiUser.grants['cluster-show-agents']) {
              authorizedTabs.push('Agents')
            }
            if (apiUser.grants['cluster-show-backups']) {
              authorizedTabs.push('Maintenance')
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
            if (apiUser.grants['cluster-grant']) {
              authorizedTabs.push('Users')
            }
            dashboardTabsRef.current = authorizedTabs
          }
        }
      }
  }, [clusterData, loggedUser])

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
      intervalTickerRef.current = 0
    }
  }, [refreshInterval])

  const renderClusterListTabWithArrow = () => {
    return (
      <>
        <CustomIcon icon={HiArrowNarrowLeft} /> Clusters
      </>
    )
  }

  const refreshMaintenanceData = (clusterName) => {
    dispatch(getResticSnapshot({ clusterName, filter: 'latest-per-session' }))
    dispatch(getBackups({ clusterName }))
    dispatch(getBackupStats({ clusterName }))
    dispatch(getResticCurrentTask({ clusterName }))
    dispatch(getJobs({ clusterName }))
  }

  const callServices = () => {
    const isAutoReloadPaused = localStorage.getItem('pause_auto_reload')

    if (!isClusterOpenRef.current) {
      if (
        globalTabsRef.current[selectedTabRef.current] === 'Clusters Local' ||
        globalTabsRef.current[selectedTabRef.current] === 'Settings'
      ) {
        if (!isAutoReloadPaused) {
          dispatch(getMonitoredData({}))
          dispatch(getClusters({}))
        }
      }
      if (
        globalTabsRef.current[selectedTabRef.current] === 'Clusters Peer' ||
        globalTabsRef.current[selectedTabRef.current] === 'Clusters For Sale'
      ) {
        dispatch(getClusterPeers({}))
        dispatch(getClusterForSale({}))
      }
      if (globalTabsRef.current[selectedTabRef.current] === 'Dashboard') {
        if (!isAutoReloadPaused) {
          dispatch(getGlobalAlerts({}))
          dispatch(getGlobalMetrics({}))
          dispatch(getGlobalLogs({}))
        }
      }
    } else if (selectedClusterNameRef.current) {
      if (!isAutoReloadPaused) {
        dispatch(getClusterData({ clusterName: selectedClusterNameRef.current }))
        dispatch(getClusterLogs({ clusterName: selectedClusterNameRef.current }))
        dispatch(getClusterAlerts({ clusterName: selectedClusterNameRef.current }))
        dispatch(getClusterMaster({ clusterName: selectedClusterNameRef.current }))
        dispatch(getClusterServers({ clusterName: selectedClusterNameRef.current }))
        dispatch(getClusterProxies({ clusterName: selectedClusterNameRef.current }))
        dispatch(getClusterApps({ clusterName: selectedClusterNameRef.current }))
        if (intervalTickerRef.current % 10 === 0) { // every 10 ticks, call the monitor data to update global level options
          dispatch(getMonitoredData({}))
        }
      }
      if (dashboardTabsRef.current[selectedTabRef.current - 1] === 'Configs') {
        dispatch(getClusterCertificates({ clusterName: selectedClusterNameRef.current }))
      }
      if (dashboardTabsRef.current[selectedTabRef.current - 1] === 'Maintenance') {
        refreshMaintenanceData(selectedClusterNameRef.current)
      }
      if (dashboardTabsRef.current[selectedTabRef.current - 1] === 'Tops') {
        dispatch(getTopProcess({ clusterName: selectedClusterNameRef.current }))
        dispatch(getOpenSVCStats({ clusterName: selectedClusterNameRef.current }))
      }
      if (dashboardTabsRef.current[selectedTabRef.current - 1] === 'Query Rules') {
        dispatch(getQueryRules({ clusterName: selectedClusterNameRef.current }))
      }
      if (dashboardTabsRef.current[selectedTabRef.current - 1] === 'Shards') {
        dispatch(getShardSchema({ clusterName: selectedClusterNameRef.current }))
      }
    }

    intervalTickerRef.current += 1
  }
  const handleTabChange = (tabIndex) => {
    // Clear settings filter when user manually clicks a tab
    localStorage.removeItem('settingsFilter')
    selectedTabRef.current = tabIndex
    setSelectedTab(tabIndex)
    if (tabIndex === 0) {
      isClusterOpenRef.current = false
      dispatch(setCluster({ data: null }))
      dispatch(setBaseURL({ baseURL: '' }))
      selectedClusterNameRef.current = ''
    } else if (
      isClusterOpenRef.current &&
      selectedClusterNameRef.current &&
      dashboardTabsRef.current[tabIndex - 1] === 'Maintenance'
    ) {
      refreshMaintenanceData(selectedClusterNameRef.current)
    }
  }

  const setDashboardTab = (cluster) => {
    selectedTabRef.current = 1
    isClusterOpenRef.current = true
    if (selectedClusterNameRef.current !== cluster.name) {
      dispatch(clearCluster({}))
    }
    selectedClusterNameRef.current = cluster.name
    setSelectedTab(1)
  }

  const openMaintenanceScheduler = () => {
    localStorage.setItem('isSchedulerOpen', JSON.stringify(true))
    const maintenanceIndex = dashboardTabsRef.current.indexOf('Maintenance')
    if (maintenanceIndex >= 0) {
      const tabIndex = maintenanceIndex + 1
      selectedTabRef.current = tabIndex
      setSelectedTab(tabIndex)
      if (selectedClusterNameRef.current) {
        refreshMaintenanceData(selectedClusterNameRef.current)
      }
    }
  }

  const openSettingsSection = (visibleSections) => {
    // Store filter so Settings page only shows the requested sections
    localStorage.setItem('settingsFilter', JSON.stringify(visibleSections))
    // Open the filtered sections, close everything else
    const allSections = [
      'isGeneralOpen', 'isRepFailOverOpen', 'isRepConfigOpen', 'isMonitoringOpen',
      'isAlertsOpen', 'isLogsOpen', 'isPluginsOpen', 'isRejoinOpen', 'isProxiesOpen',
      'isGraphsOpen', 'isCloud18Open', 'isSchedulerOpen', 'isBackupOpen',
      'isS3ProvidersOpen', 'isAppTemplateRepoOpen'
    ]
    allSections.forEach((key) => localStorage.setItem(key, JSON.stringify(visibleSections.includes(key))))
    const settingsIndex = dashboardTabsRef.current.indexOf('Settings')
    if (settingsIndex >= 0) {
      const tabIndex = settingsIndex + 1
      selectedTabRef.current = tabIndex
      setSelectedTab(tabIndex)
    }
  }

  const openBackupSettings = () => openSettingsSection(['isBackupOpen', 'isS3ProvidersOpen'])
  const openSchedulerSettings = () => openSettingsSection(['isSchedulerOpen'])
  const openTopologySettings = () => openSettingsSection(['isGeneralOpen'])
  const openProxiesSettings = () => openSettingsSection(['isProxiesOpen'])
  const openLogsSettings = () => openSettingsSection(['isLogsOpen'])
  const openMonitoringSettings = () => openSettingsSection(['isMonitoringOpen'])
  const openPluginsSettings = () => openSettingsSection(['isPluginsOpen'])
  const openGraphsSettings = () => openSettingsSection(['isGraphsOpen'])
  const openFailoverSettings = () => openSettingsSection(['isRepFailOverOpen'])
  const openNewClusterModal = (e) => {
    e.stopPropagation()
    setIsNewClusterModalOpen(true)
    setSelectedTab(0)
    dispatch(pauseAutoReload({ isPaused: true }))
  }

  const closeNewClusterModal = () => {
    setIsNewClusterModalOpen(false)
    dispatch(pauseAutoReload({ isPaused: false }))
  }

  const openTerminalPage = () => {
    window.open(terminalURL, '_blank')
  }

  return (
    <PageContainer>
      <Box className={styles.container}>
        <TabItems
          tabIndex={selectedTab}
          onChange={handleTabChange}
          options={
            isClusterOpenRef.current
              ? [renderClusterListTabWithArrow(), ...dashboardTabsRef.current]
              : globalTabsRef.current
          }
          tabPrefix={[...(selectedClusterNameRef.current == '' && localStorage.getItem('username') == "admin" ? [<div onClick={openNewClusterModal} className={styles.tabSelected}><CustomIcon icon={FaPlus} /></div>] : [])]}
          tabSuffix={[...(selectedClusterNameRef.current == '' && localStorage.getItem('username') == "admin" ? [<div onClick={openTerminalPage} className={styles.tabNormal}>Terminal</div>] : [])]}
          tabContents={[
            <ClusterList onClick={setDashboardTab} />,
            ...(isClusterOpenRef.current
              ? [
                <Dashboard user={user} selectedCluster={selectedCluster} openSettings={{ topology: openTopologySettings, proxies: openProxiesSettings, logs: openLogsSettings, scheduler: openSchedulerSettings, monitoring: openMonitoringSettings, plugins: openPluginsSettings, graphs: openGraphsSettings, failover: openFailoverSettings }} />,
                <Settings user={user} selectedCluster={selectedCluster} onTabChange={handleTabChange} monitor={monitor} />,
                <Configs user={user} selectedCluster={selectedCluster} />,
                ...(selectedCluster?.config?.graphiteMetrics && user?.grants['cluster-show-graphs']
                  ? [<Graphs selectedCluster={selectedCluster} onOpenSettings={openGraphsSettings} />]
                  : []),
                ...(user?.grants['cluster-show-agents']
                  ? [<Agents user={user} selectedCluster={selectedCluster} />]
                  : []),
                ...(user?.grants['cluster-show-backups']
                  ? [<Maintenance user={user} selectedCluster={selectedCluster} onOpenBackupSettings={openBackupSettings} onOpenSchedulerSettings={openSchedulerSettings} onOpenLogsSettings={openLogsSettings} />]
                  : []),
                ...(user?.grants['db-show-process'] ? [<Top selectedCluster={selectedCluster} />] : []),
                ...(selectedCluster?.config?.proxysql && user?.grants['cluster-show-agents']
                  ? [<QueryRules selectedCluster={selectedCluster} />]
                  : []),
                ...(user?.grants['db-show-schema'] ? [<Shards selectedCluster={selectedCluster} user={user} onOpenSchedulerSettings={openMaintenanceScheduler} onOpenLogsSettings={openLogsSettings} />] : []),
                ...(user?.grants['cluster-grant'] ? [<Users selectedCluster={selectedCluster} user={user} />] : [])
              ]
              : [
                  ...(globalTabsRef.current.includes('Clusters Peer')
                    ? [<PeerClusterList onLogin={setDashboardTab} />, <PeerClusterList mode='shared' />]
                    : []),
                  ...(globalTabsRef.current.includes('Billing')
                    ? [<Billing />]
                    : []),
                  ...(globalTabsRef.current.includes('Settings')
                    ? [<ClustersGlobalSettings user={loggedUser} />]
                    : []),
                  <GlobalItems />
                ])
          ]}
        />
        {
          selectedClusterNameRef.current == '' && (
            <>
              {isNewClusterModalOpen && (
                <NewClusterModal plans={monitor?.servicePlans} orchestrators={monitor?.serviceOrchestrators} defaultOrchestrator={monitor?.config.provOrchestrator} isOpen={isNewClusterModalOpen} closeModal={closeNewClusterModal} />
              )}
            </>
          )
        }
      </Box>
    </PageContainer>
  )
}

export default Home
