import { useEffect, useMemo, useState } from 'react'
import styles from './styles.module.scss'
import { Flex, HStack, VStack } from '@chakra-ui/react'
import AppMenu from '../../../Dashboard/components/Apps/AppMenu'
import ServerName from '../../../../components/ServerName'
import Overview from '../Overview'
import ServiceOpenSvc from '../../../ClusterDB/components/ServiceOpenSvc'
import ServerStatus from '../../../../components/ServerStatus'
import StoragePage from '../Storage'

function ClusterAppTabContent({ appId, tab, clusterName, user, selectedApp, config }) {
  const [currentTab, setCurrentTab] = useState('')
  const appConfig = selectedApp?.config
  const appName = selectedApp?.name
  const appHost = selectedApp?.host

  if (!selectedApp) {
    return (
        <Flex className={styles.actions}>
          <HStack>
            <ServerName className={styles.appName} name="Loading selected app" />
          </HStack>
        </Flex>
    )
  }

  const overviewComponent = useMemo(() => {
    return (
      <Overview
        clusterName={clusterName}
        appId={appId}
        appName={appName}
        appHost={appHost}
        appConfig={appConfig}
        config={config}
        user={user}
      />
    )
  }, [clusterName, appId, appName, appHost, appConfig, config, user])

  const storagesComponent = useMemo(() => {
    return (
      <StoragePage
        clusterName={clusterName}
        appId={appId}
        user={user}
      />
    )
  }, [clusterName, appId, user])

  const serviceOpenSvcComponent = useMemo(() => {
    return (
      <ServiceOpenSvc
        clusterName={clusterName}
        type="app"
        id={appId}  
        user={user}
      />
    )
  }, [clusterName, appId, user])



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
      {currentTab === "overview" ? (overviewComponent) 
        : currentTab === "storages" ? (storagesComponent)
        : currentTab === "opensvc" ? (serviceOpenSvcComponent) 
        : null }
    </VStack>
  )
}

export default ClusterAppTabContent
