import React from 'react'
import AccordionComponent from '../../components/AccordionComponent'
import styles from './styles.module.scss'
import DBConfigs from './components/DBConfigs'
import ProxyConfig from './components/ProxyConfig'
import { VStack } from '@chakra-ui/react'

function Configs({ selectedCluster, user }) {
  return (
    <VStack className={styles.configContainer}>
      <AccordionComponent
        className={styles.accordion}
        headerClassName={styles.accordionHeader}
        heading={'Database Configurator'}
        body={<DBConfigs selectedCluster={selectedCluster} user={user} />}
      />
      <AccordionComponent
        className={styles.accordion}
        headerClassName={styles.accordionHeader}
        heading={'Proxy Configurator'}
        body={<ProxyConfig selectedCluster={selectedCluster} user={user} />}
      />
    </VStack>
  )
}

export default Configs
