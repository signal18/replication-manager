import React, { useEffect, useState } from 'react'
import ProcessList from '../ProcessList'
import { useSelector } from 'react-redux'
import styles from './styles.module.scss'
import { Flex, HStack, VStack, Text } from '@chakra-ui/react'
import ServerMenu from '../../../Dashboard/components/DBServers/ServerMenu'
import ServerStatus from '../../../../components/ServerStatus'
import ServerName from '../../../../components/ServerName'
import SlowQueries from '../SlowQueries'
import DigestQueries from '../DigestQueries'
import Tables from '../Tables'

function ClusterDBTabContent({ tab, dbId, clusterName, digestMode, toggleDigestMode }) {
  const [currentTab, setCurrentTab] = useState('')
  const [selectedDBServer, setSelectedDBServer] = useState(null)
  const [user, setUser] = useState(null)

  const {
    cluster: { clusterMaster, clusterServers, clusterData }
  } = useSelector((state) => state)

  useEffect(() => {
    setCurrentTab(tab)
  }, [tab])

  useEffect(() => {
    if (clusterServers?.length > 0 && dbId) {
      const server = clusterServers.find((x) => x.id === dbId)
      setSelectedDBServer(server)
    }
    if (clusterData?.apiUsers) {
      const loggedUser = localStorage.getItem('username')
      if (loggedUser && clusterData?.apiUsers[loggedUser]) {
        const apiUser = clusterData.apiUsers[loggedUser]
        setUser(apiUser)
      }
    }
  }, [dbId, clusterServers])

  return (
    <VStack className={styles.contentContainer}>
      <Flex className={styles.actions}>
        <HStack>
          {selectedDBServer && (
            <>
              <ServerMenu
                clusterName={clusterName}
                clusterMasterId={clusterMaster?.id}
                backupLogicalType={clusterData?.config?.backupLogicalType}
                backupPhysicalType={clusterData?.config?.backupPhysicalType}
                row={selectedDBServer}
                user={user}
                showCompareWithOption={false}
              />
              <ServerStatus state={selectedDBServer?.state} />
              <ServerName className={styles.serverName} name={`${selectedDBServer?.host}:${selectedDBServer?.port}`} />
              {/* <Text className={styles.serverName}>{`${selectedDBServer?.host}:${selectedDBServer?.port}`}</Text> */}
            </>
          )}
        </HStack>
      </Flex>
      {currentTab === 'processlist' ? (
        <ProcessList clusterName={clusterName} dbId={dbId} />
      ) : currentTab === 'slowqueries' ? (
        <SlowQueries clusterName={clusterName} dbId={dbId} selectedDBServer={selectedDBServer} />
      ) : currentTab === 'digestqueries' ? (
        <DigestQueries
          clusterName={clusterName}
          dbId={dbId}
          selectedDBServer={selectedDBServer}
          digestMode={digestMode}
          toggleDigestMode={toggleDigestMode}
        />
      ) : currentTab === 'errors' ? (
        <div>errors</div>
      ) : currentTab === 'tables' ? (
        clusterData?.workLoad?.dbTableSize >= 0 ? (
          <Tables
            clusterName={clusterName}
            dbId={dbId}
            selectedDBServer={selectedDBServer}
            tableSize={clusterData?.workLoad?.dbTableSize}
          />
        ) : null
      ) : currentTab === 'status' ? (
        <div>status</div>
      ) : null}
    </VStack>
  )
}

export default ClusterDBTabContent
