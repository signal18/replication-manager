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
import ServerAudit from '../ServerAudit'
import PFSInstruments from '../PFSInstruments'

function ClusterDBTabContent({ tab, dbId, clusterName, digestMode, toggleDigestMode, user, selectedDBServer, variableMode, toggleVariableMode, onNavigateToPFSInstruments, onNavigateToVariables, variablesSearchFilter }) {
  const [currentTab, setCurrentTab] = useState('')

  const {
    cluster: { clusterMaster, clusterData, database }
  } = useSelector((state) => state)

  // Get the performance_schema_instrument configuration value
  const pfsConfigValue = database?.variables?.find(
    v => v.variableName === 'performance_schema_instrument'
  )?.runtimeValue || database?.variables?.find(
    v => v.variableName === 'performance_schema_instrument'
  )?.value || null

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
                orchestrator={clusterData?.config?.provOrchestrator}
                row={selectedDBServer}
                user={user}
                showCompareWithOption={false}
                showTerminal={clusterData?.config?.terminalSessionEnabled}
              />
              <ServerStatus state={selectedDBServer?.state} isVirtualMaster={selectedDBServer?.isVirtualMaster} />
              <ServerName className={styles.serverName} name={`${selectedDBServer?.host}:${selectedDBServer?.port}`} />
            </>
          )}
        </HStack>
      </Flex>
      {currentTab === 'processlist' ? (
        <ProcessList clusterName={clusterName} dbId={dbId} user={user}  />
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
        <Errors clusterName={clusterName} dbId={dbId} selectedDBServer={selectedDBServer} />
      ) : currentTab === 'auditlogs' ? (
        <ServerAudit clusterName={clusterName} dbId={dbId} selectedDBServer={selectedDBServer} />
      ) : currentTab === 'tables' ? (
        clusterData?.workLoad?.dbTableSize >= 0 ? (
          <Tables
            clusterName={clusterName}
            dbId={dbId}
            selectedDBServer={selectedDBServer}
            tableSize={clusterData?.workLoad?.dbTableSize}
            usePersistent={clusterData?.config?.analyzeUsePersistent}
          />
        ) : null
      ) : currentTab === 'status' ? (
        <Status clusterName={clusterName} dbId={dbId} />
      ) : currentTab === 'variables' ? (
        <Variables 
          clusterName={clusterName} 
          dbId={dbId} 
          toggleVariableMode={toggleVariableMode} 
          variableMode={variableMode} 
          onNavigateToPFSInstruments={onNavigateToPFSInstruments}
          searchFilter={variablesSearchFilter}
        />
      ) : currentTab === 'opensvc' ? (
        <ServiceOpenSvc clusterName={clusterName} type="db" id={dbId} />
      ) : currentTab === 'metadata' ? (
        <MetadataLocks clusterName={clusterName} dbId={dbId} selectedDBServer={selectedDBServer} />
      ) : currentTab === 'resptime' ? (
        <ResponseTime clusterName={clusterName} dbId={dbId} selectedDBServer={selectedDBServer} />
      ) : currentTab === 'pfsinstruments' ? (
        <PFSInstruments 
          selectedDBServer={selectedDBServer} 
          pfsConfigValue={pfsConfigValue}
          onNavigateToVariables={onNavigateToVariables}
        />
      ) : null}
    </VStack>
  )
}

export default ClusterDBTabContent
