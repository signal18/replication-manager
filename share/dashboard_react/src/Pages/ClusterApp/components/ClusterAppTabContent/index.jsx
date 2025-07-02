import { useEffect, useMemo, useState } from 'react'
import styles from './styles.module.scss'
import { Flex, HStack, VStack } from '@chakra-ui/react'
import AppMenu from '../../../Dashboard/components/Apps/AppMenu'
import ServerName from '../../../../components/ServerName'
import Deployment from '../Deployment'
import ServiceOpenSvc from '../../../ClusterDB/components/ServiceOpenSvc'
import ServerStatus from '../../../../components/ServerStatus'

function ClusterAppTabContent({ appId, tab, clusterName, user, selectedApp, config }) {
  const [currentTab, setCurrentTab] = useState('')
  const appConfig = selectedApp?.config
  const appName = selectedApp?.name
  const appHost = selectedApp?.host

  const deploymentComponent = useMemo(() => {
    return (
      <Deployment
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
      {currentTab === "overview" ? (deploymentComponent) 
        : currentTab === "opensvc" ? (serviceOpenSvcComponent) 
        : null }
    </VStack>
  )
}

export default ClusterAppTabContent
