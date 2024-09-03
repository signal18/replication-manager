import React, { useState } from 'react'
import { Link, useNavigate, useParams } from 'react-router-dom'
import PageContainer from '../PageContainer'
import styles from './styles.module.scss'
import TabItems from '../../components/TabItems'
import ClusterDBTabContent from './components/ClusterDBTabContent'
import { Box } from '@chakra-ui/react'
import CustomIcon from '../../components/Icons/CustomIcon'
import { HiArrowNarrowLeft } from 'react-icons/hi'

function ClusterDB(props) {
  const params = useParams()
  const navigate = useNavigate()
  const [selectedTab, setSelectedTab] = useState(1)
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

  const handleTabChange = (tabIndex) => {
    setSelectedTab(tabIndex)
    if (tabIndex === 0) {
      navigate(`/clusters/${params.cluster}`)
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
            <ClusterDBTabContent tab='processlist' />,
            <ClusterDBTabContent tab='slowqueries' />,
            <ClusterDBTabContent tab='digestqueries' />,
            <ClusterDBTabContent tab='errors' />,
            <ClusterDBTabContent tab='tables' />,
            <ClusterDBTabContent tab='status' />
          ]}
        />
      </Box>
    </PageContainer>
  )
}

export default ClusterDB
