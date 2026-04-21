import { useEffect, useMemo, useState } from 'react'
import PropTypes from 'prop-types'
import styles from './styles.module.scss'
import { Flex, HStack, VStack } from '@chakra-ui/react'
import AppMenu from '../../../Dashboard/components/Apps/AppMenu'
import ServerName from '../../../../components/ServerName'
import Overview from '../Overview'
import ServiceOpenSvc from '../../../ClusterDB/components/ServiceOpenSvc'
import ServerStatus from '../../../../components/ServerStatus'
import StoragePage from '../Storage'
import Templates from '../Templates'

function ClusterAppTabContent({ appId, tab, clusterName, user, selectedApp, config }) {
  const [currentTab, setCurrentTab] = useState('')
  const appConfig = selectedApp?.config
  const appName = selectedApp?.name
  const appHost = selectedApp?.host

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

  const templatesComponent = useMemo(() => {
    return (
      <Templates
        clusterName={clusterName}
        appConfig={appConfig}
        user={user}
      />
    )
  }, [clusterName, appConfig, user])


  useEffect(() => {
    setCurrentTab(tab)
  }, [tab])

  if (!selectedApp) {
    return (
      <Flex className={styles.actions}>
        <HStack>
          <ServerName className={styles.appName} name="Loading selected app" />
        </HStack>
      </Flex>
    )
  }

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
                orchestrator={config?.provOrchestrator}
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
        : currentTab === "templates" ? (templatesComponent)
        : null }
    </VStack>
  )
}

export default ClusterAppTabContent

ClusterAppTabContent.propTypes = {
  appId: PropTypes.string.isRequired,
  tab: PropTypes.string.isRequired,
  clusterName: PropTypes.string.isRequired,
  user: PropTypes.shape({
    grants: PropTypes.object
  }),
  selectedApp: PropTypes.shape({
    config: PropTypes.object,
    name: PropTypes.string,
    host: PropTypes.string,
    state: PropTypes.string
  }),
  config: PropTypes.object
}
