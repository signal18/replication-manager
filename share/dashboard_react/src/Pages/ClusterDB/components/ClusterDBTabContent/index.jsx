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
import Status from '../Status'
import Variables from '../Variables'
import ServiceOpenSvc from '../ServiceOpenSvc'
import MetadataLocks from '../MetadataLocks'
import ResponseTime from '../ResponseTime'
import Errors from '../Errors'

function ClusterDBTabContent({ tab, dbId, clusterName, digestMode, toggleDigestMode, user, selectedDBServer }) {
  const [currentTab, setCurrentTab] = useState('')

  const {
    cluster: { clusterMaster, clusterData }
  } = useSelector((state) => state)

  useEffect(() => {
    setCurrentTab(tab)
  }, [tab])

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
        <Errors selectedDBServer={selectedDBServer} />
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
        <Status clusterName={clusterName} dbId={dbId} />
      ) : currentTab === 'variables' ? (
        <Variables clusterName={clusterName} dbId={dbId} />
      ) : currentTab === 'opensvc' ? (
        <ServiceOpenSvc clusterName={clusterName} dbId={dbId} />
      ) : currentTab === 'metadata' ? (
        <MetadataLocks clusterName={clusterName} dbId={dbId} />
      ) : currentTab === 'resptime' ? (
        <ResponseTime clusterName={clusterName} dbId={dbId} />
      ) : null}
    </VStack>
  )
}

export default ClusterDBTabContent
