import React, { useEffect, useState } from 'react'
import styles from './styles.module.scss'
import { Flex, HStack, VStack } from '@chakra-ui/react'
import AppMenu from '../../../Dashboard/components/Apps/AppMenu'
import AppStatus from '../../../Dashboard/components/Apps/AppStatus'
import ServerName from '../../../../components/ServerName'
import Deployment from '../Deployment'
import ServiceOpenSvc from '../../../ClusterDB/components/ServiceOpenSvc'
import ServerStatus from '../../../../components/ServerStatus'

function ClusterAppTabContent({ appId, tab, clusterName, user, selectedApp, config }) {
  const [currentTab, setCurrentTab] = useState('')
  const appConfig = selectedApp?.config

  useEffect(() => {
    setCurrentTab(tab)
  }, [tab])

  return (
    <VStack className={styles.contentContainer}>
      <Flex className={styles.actions}>
        <HStack>
          {selectedApp && (
            <>
              <AppMenu
                clusterName={clusterName}
                row={selectedApp}
                user={user}
              />
              <ServerStatus state={selectedApp?.state} />
              <ServerName className={styles.appName} name={`${selectedApp?.host}`} />
            </>
          )}
        </HStack>
      </Flex>
      {currentTab === "overview" ? (
        <Deployment clusterName={clusterName} appId={appId} appConfig={appConfig} config={config} user={user}/>
      ) : currentTab === "opensvc" ? (
        <ServiceOpenSvc clusterName={clusterName} type="app" id={appId} user={user}/>
      ) : null }
    </VStack>
  )
}

export default ClusterAppTabContent
