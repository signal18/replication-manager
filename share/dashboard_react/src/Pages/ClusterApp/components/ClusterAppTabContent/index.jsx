import React, { useEffect, useState } from 'react'
import styles from './styles.module.scss'
import { Flex, HStack, VStack } from '@chakra-ui/react'
import AppMenu from '../../../Dashboard/components/Apps/AppMenu'
import AppStatus from '../../../Dashboard/components/Apps/AppStatus'
import ServerName from '../../../../components/ServerName'
import Deployments from '../Deployments'

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
        <Deployments selectedApp={selectedApp}/>
      ) : null}
    </VStack>
  )
}

export default ClusterAppTabContent
