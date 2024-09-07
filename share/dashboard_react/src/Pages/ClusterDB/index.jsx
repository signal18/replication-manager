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
import { getClusterServers, getDatabaseService, setRefreshInterval } from '../../redux/clusterSlice'

function ClusterDB(props) {
  const params = useParams()
  const dispatch = useDispatch()
  const navigate = useNavigate()
  const selectedTabRef = useRef(1)
  const [selectedTab, setSelectedTab] = useState(1)
  const [clusterName, setClusterName] = useState(params.cluster)
  const [dbId, setDbId] = useState(params.dbname)
  const [tabs, setTabs] = useState([
    <>
      <CustomIcon icon={HiArrowNarrowLeft} /> Dashboard
    </>,
    'Process List',
    'Slow Queries',
    'Digest Queries',
    'Errors',
    'Tables',
    'Status',
    'Status InnoDB',
    'Variables',
    'Service OpenSVC',
    'Metadata Locks'
  ])

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

  const callServices = () => {
    dispatch(getClusterServers({ clusterName }))
    if (selectedTabRef.current === 1) {
      dispatch(getDatabaseService({ clusterName, serviceName: 'processlist', dbId }))
    }
  }

  const handleTabChange = (tabIndex) => {
    selectedTabRef.current = tabIndex
    setSelectedTab(tabIndex)
    if (tabIndex === 0) {
      navigate(`/clusters/${clusterName}`)
    }
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
            <ClusterDBTabContent tab='processlist' dbId={dbId} clusterName={clusterName} />,
            <ClusterDBTabContent tab='slowqueries' dbId={dbId} clusterName={clusterName} />,
            <ClusterDBTabContent tab='digestqueries' dbId={dbId} clusterName={clusterName} />,
            <ClusterDBTabContent tab='errors' dbId={dbId} clusterName={clusterName} />,
            <ClusterDBTabContent tab='tables' dbId={dbId} clusterName={clusterName} />,
            <ClusterDBTabContent tab='status' dbId={dbId} clusterName={clusterName} />
          ]}
        />
      </Box>
    </PageContainer>
  )
}

export default ClusterDB
