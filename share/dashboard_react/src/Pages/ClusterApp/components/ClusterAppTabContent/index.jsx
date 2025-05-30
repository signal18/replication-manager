import React, { useEffect, useState } from 'react'
import styles from './styles.module.scss'
import { Flex, HStack, VStack } from '@chakra-ui/react'
import AppMenu from '../../../Dashboard/components/Apps/AppMenu'
import AppStatus from '../../../Dashboard/components/Apps/AppStatus'
import ServerName from '../../../../components/ServerName'
import Deployment from '../Deployment'

function ClusterAppTabContent({ tab, clusterName, user, selectedApp }) {
  const [currentTab, setCurrentTab] = useState('')

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
              <AppStatus status={selectedApp?.state} />
              <ServerName className={styles.appName} name={`${selectedApp?.host}`} />
            </>
          )}
        </HStack>
      </Flex>
      {currentTab === "overview" ? (
        <Deployment clusterName={clusterName} appId={selectedApp?.id} config={selectedApp?.config}/>
      ) : null}
    </VStack>
  )
}

export default ClusterAppTabContent
