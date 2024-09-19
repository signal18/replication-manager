import React, { useEffect, useRef, useState } from 'react'
import { Link, useNavigate, useParams } from 'react-router-dom'
import PageContainer from '../PageContainer'
import styles from './styles.module.scss'
import TabItems from '../../components/TabItems'
import ClusterDBTabContent from './components/ClusterDBTabContent'
import { Box } from '@chakra-ui/react'
import CustomIcon from '../../components/Icons/CustomIcon'
import { HiArrowNarrowLeft } from 'react-icons/hi'
import { useDispatch, useSelector } from 'react-redux'
import { getClusterData, getClusterServers, getDatabaseService, setRefreshInterval } from '../../redux/clusterSlice'

function ClusterDB(props) {
  const params = useParams()
  const dispatch = useDispatch()
  const navigate = useNavigate()
  const selectedTabRef = useRef(1)
  const digestModeRef = useRef('pfs')
  const [selectedTab, setSelectedTab] = useState(1)
  const [user, setUser] = useState(null)
  const [selectedDBServer, setSelectedDBServer] = useState(null)
  const [clusterName, setClusterName] = useState(params.cluster)
  const [dbId, setDbId] = useState(params.dbname)
  const [tabs, setTabs] = useState([])

  const {
    cluster: { refreshInterval, clusterServers, clusterData }
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

  useEffect(() => {
    if (clusterServers?.length > 0 && dbId) {
      const server = clusterServers.find((x) => x.id === dbId)
      setSelectedDBServer(server)
    }
    if (clusterData?.apiUsers) {
      const loggedUser = localStorage.getItem('username')
      if (loggedUser && clusterData?.apiUsers[loggedUser]) {
        const apiUser = clusterData.apiUsers[loggedUser]
        const authorizedTabs = [
          <>
            <CustomIcon icon={HiArrowNarrowLeft} /> Dashboard
          </>
        ]
        if (apiUser.grants['db-show-process']) {
          authorizedTabs.push('Process List')
        }
        if (apiUser.grants['db-show-logs']) {
          authorizedTabs.push('Slow Queries')
          authorizedTabs.push('Digest Queries')
          authorizedTabs.push('Errors')
        }
        if (apiUser.grants['db-show-schema']) {
          authorizedTabs.push('Tables')
        }
        if (apiUser.grants['db-show-status']) {
          authorizedTabs.push('Status')
        }
        if (apiUser.grants['db-show-variables']) {
          authorizedTabs.push('Variables')
        }
        authorizedTabs.push('Service OpenSVC')
        if (apiUser.grants['db-show-logs']) {
          authorizedTabs.push('Metadata Locks')
          authorizedTabs.push('Response Time')
        }
        setTabs(authorizedTabs)
        setUser(apiUser)
      }
    }
  }, [dbId, clusterServers])

  const callServices = () => {
    dispatch(getClusterServers({ clusterName }))
    dispatch(getClusterData({ clusterName }))
    if (selectedTabRef.current === 1) {
      dispatch(getDatabaseService({ clusterName, serviceName: 'processlist', dbId }))
    }
    if (selectedTabRef.current === 2) {
      dispatch(getDatabaseService({ clusterName, serviceName: 'slow-queries', dbId }))
    }
    if (selectedTabRef.current === 3) {
      if (digestModeRef.current === 'pfs') {
        dispatch(getDatabaseService({ clusterName, serviceName: 'digest-statements-pfs', dbId }))
      } else {
        dispatch(getDatabaseService({ clusterName, serviceName: 'digest-statements-slow', dbId }))
      }
    }
    if (selectedTabRef.current === 5) {
      dispatch(getDatabaseService({ clusterName, serviceName: 'tables', dbId }))
    }
    if (selectedTabRef.current === 6) {
      dispatch(getDatabaseService({ clusterName, serviceName: 'status-delta', dbId }))
      dispatch(getDatabaseService({ clusterName, serviceName: 'status-innodb', dbId }))
    }
    if (selectedTabRef.current === 7) {
      dispatch(getDatabaseService({ clusterName, serviceName: 'variables', dbId }))
    }
    if (selectedTabRef.current === 8) {
      dispatch(getDatabaseService({ clusterName, serviceName: 'service-opensvc', dbId }))
    }
    if (selectedTabRef.current === 9) {
      dispatch(getDatabaseService({ clusterName, serviceName: 'meta-data-locks', dbId }))
    }
    if (selectedTabRef.current === 10) {
      dispatch(getDatabaseService({ clusterName, serviceName: 'query-response-time', dbId }))
    }
  }

  const handleTabChange = (tabIndex) => {
    selectedTabRef.current = tabIndex
    setSelectedTab(tabIndex)
    if (tabIndex === 0) {
      navigate(`/clusters/${clusterName}`)
    }
  }

  const toggleDigestMode = () => {
    digestModeRef.current = digestModeRef.current === 'pfs' ? 'slow' : 'pfs'
  }

  return (
    <PageContainer>
      <Box className={styles.container}>
        <TabItems
          tabIndex={selectedTab}
          onChange={handleTabChange}
          options={tabs}
          className={styles.tabs}
          tabContents={[
            null,
            ...(user?.grants['db-show-process']
              ? [
                  <ClusterDBTabContent
                    tab='processlist'
                    dbId={dbId}
                    clusterName={clusterName}
                    user={user}
                    selectedDBServer={selectedDBServer}
                  />
                ]
              : []),
            ...(user?.grants['db-show-logs']
              ? [
                  <ClusterDBTabContent
                    tab='slowqueries'
                    dbId={dbId}
                    clusterName={clusterName}
                    user={user}
                    selectedDBServer={selectedDBServer}
                  />,
                  <ClusterDBTabContent
                    tab='digestqueries'
                    dbId={dbId}
                    clusterName={clusterName}
                    digestMode={digestModeRef.current}
                    toggleDigestMode={toggleDigestMode}
                    user={user}
                    selectedDBServer={selectedDBServer}
                  />,
                  <ClusterDBTabContent
                    tab='errors'
                    dbId={dbId}
                    clusterName={clusterName}
                    user={user}
                    selectedDBServer={selectedDBServer}
                  />
                ]
              : []),
            ...(user?.grants['db-show-schema']
              ? [
                  <ClusterDBTabContent
                    tab='tables'
                    dbId={dbId}
                    clusterName={clusterName}
                    user={user}
                    selectedDBServer={selectedDBServer}
                  />
                ]
              : []),
            ...(user?.grants['db-show-status']
              ? [
                  <ClusterDBTabContent
                    tab='status'
                    dbId={dbId}
                    clusterName={clusterName}
                    user={user}
                    selectedDBServer={selectedDBServer}
                  />
                ]
              : []),
            ...(user?.grants['db-show-variables']
              ? [
                  <ClusterDBTabContent
                    tab='variables'
                    dbId={dbId}
                    clusterName={clusterName}
                    user={user}
                    selectedDBServer={selectedDBServer}
                  />
                ]
              : []),

            <ClusterDBTabContent
              tab='opensvc'
              dbId={dbId}
              clusterName={clusterName}
              user={user}
              selectedDBServer={selectedDBServer}
            />,

            ...(user?.grants['db-show-logs']
              ? [
                  <ClusterDBTabContent
                    tab='metadata'
                    dbId={dbId}
                    clusterName={clusterName}
                    user={user}
                    selectedDBServer={selectedDBServer}
                  />,
                  <ClusterDBTabContent
                    tab='resptime'
                    dbId={dbId}
                    clusterName={clusterName}
                    user={user}
                    selectedDBServer={selectedDBServer}
                  />
                ]
              : [])
          ]}
        />
      </Box>
    </PageContainer>
  )
}

export default ClusterDB
