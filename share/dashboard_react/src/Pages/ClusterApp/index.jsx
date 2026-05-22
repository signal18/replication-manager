import { useEffect, useRef, useState } from 'react'
import { useNavigate, useParams } from 'react-router-dom'
import PageContainer from '../PageContainer'
import styles from './styles.module.scss'
import TabItems from '../../components/TabItems'
import ClusterAppTabContent from './components/ClusterAppTabContent'
import { Box, Flex, Text } from '@chakra-ui/react'
import CustomIcon from '../../components/Icons/CustomIcon'
import { HiArrowNarrowLeft } from 'react-icons/hi'
import { useDispatch, useSelector } from 'react-redux'
import { getClusterData, getClusterApps, setRefreshInterval, getAppService } from '../../redux/clusterSlice'
import { refreshAppTemplateRepo } from '../../redux/globalClustersSlice'
import { AppSettings } from '../../AppSettings'
import { isAutoReloadPaused } from '../../utility/autoReloadPause'

function ClusterApp() {
  const params = useParams()
  const dispatch = useDispatch()
  const navigate = useNavigate()
  const selectedTabRef = useRef(1)
  const [selectedTab, setSelectedTab] = useState(1)
  const [user, setUser] = useState(null)
  const [selectedApp, setSelectedApp] = useState(null)
	const clusterName = params.cluster
	const appId = params.appname
  const tabs = useRef([])

  const loggedUser = useSelector((state) => state.auth.user)
  const refreshInterval = useSelector((state) => state.cluster.refreshInterval)
  const clusterApps = useSelector((state) => state.cluster.clusterApps)
  const clusterData = useSelector((state) => state.cluster.clusterData)

  const callServices = () => {
    dispatch(getClusterApps({ clusterName }))
    dispatch(getClusterData({ clusterName }))
    if (!isAutoReloadPaused()) {
      if (tabs.current[selectedTabRef.current] === 'App Overview') {
        dispatch(getAppService({ clusterName, serviceName: 'deployment', appId }))
        dispatch(getAppService({ clusterName, serviceName: 'substitution', appId }))
      }
      if (tabs.current[selectedTabRef.current] === 'Storages') {
        dispatch(getAppService({ clusterName, serviceName: 'deployment', appId }))
      }
      if (tabs.current[selectedTabRef.current] === 'Service OpenSVC') {
        dispatch(getAppService({ clusterName, serviceName: 'service-opensvc', appId }))
      }
      if (tabs.current[selectedTabRef.current] === 'Templates') {
        dispatch(refreshAppTemplateRepo({ clusterName, silent: true }))
      }
    }
  }


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


  useEffect(() => {
    if (clusterApps?.length === 0) {
      callServices()
      return
    }

    if (clusterApps?.length > 0 && appId) {
      const app = clusterApps.find((x) => x.id === appId)
      setSelectedApp(app)
    }
    if (clusterData?.apiUsers && loggedUser) {
        const apiUser = clusterData?.apiUsers[loggedUser.User] || clusterData?.apiUsers[loggedUser.Email]
        if (apiUser) {
          const authorizedTabs = [
            <>
              <CustomIcon icon={HiArrowNarrowLeft} /> Dashboard
            </>
          ]
          authorizedTabs.push('App Overview')
          authorizedTabs.push('Storages')
          authorizedTabs.push('Service OpenSVC')
          authorizedTabs.push('Templates')
          tabs.current = authorizedTabs
          setUser(apiUser)
        }
    }
  }, [appId, clusterApps, loggedUser])


  const handleTabChange = (tabIndex) => {
    selectedTabRef.current = tabIndex
    setSelectedTab(tabIndex)
    if (tabIndex === 0) {
      navigate(`/clusters/${clusterName}`)
    }
  }

  if (clusterApps?.length === 0 || !selectedApp) {
    return (
      <PageContainer>
        <Box className={styles.container}>
          <Flex className={styles.actions}>
            <Text>Loading selected app...</Text>
          </Flex>
        </Box>
      </PageContainer>
    )
  }

  return (
    <PageContainer>
      <Box className={styles.container}>
        <TabItems
          tabIndex={selectedTab}
          onChange={handleTabChange}
          options={tabs.current}
          className={styles.tabs}
          tabContents={[
            null,
            <ClusterAppTabContent
              key='cluster-app-tab-overview'
              tab='overview'
              appId={appId}
              clusterName={clusterName}
              user={user}
              selectedApp={selectedApp}
              config={clusterData?.config}
            />,
            <ClusterAppTabContent
              key='cluster-app-tab-storages'
              tab='storages'
              appId={appId}
              clusterName={clusterName}
              user={user}
              selectedApp={selectedApp}
              config={clusterData?.config}
            />,
            <ClusterAppTabContent
              key='cluster-app-tab-opensvc'
              tab='opensvc'
              appId={appId}
              clusterName={clusterName}
              user={user}
              selectedApp={selectedApp}
              config={clusterData?.config}
            />,
            <ClusterAppTabContent
              key='cluster-app-tab-templates'
              tab='templates'
              appId={appId}
              clusterName={clusterName}
              user={user}
              selectedApp={selectedApp}
              config={clusterData?.config}
            />,
          ]}
        />
      </Box>
    </PageContainer>
  )
}

export default ClusterApp
