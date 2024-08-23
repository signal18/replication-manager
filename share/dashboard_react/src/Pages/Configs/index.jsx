import React from 'react'
import AccordionComponent from '../../components/AccordionComponent'
import styles from './styles.module.scss'
import DBConfigs from './components/DBConfigs'
import ProxyConfig from './components/ProxyConfig'
import { VStack } from '@chakra-ui/react'
import OrchestratorImages from './components/OrchestratorImages'
import OrchestratorDisks from './components/OrchestratorDisks'
import OrchestratorDbVM from './components/OrchestratorDbVM'

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
      <AccordionComponent
        className={styles.accordion}
        headerClassName={styles.accordionHeader}
        heading={'Orchestrator Images'}
        body={<OrchestratorImages selectedCluster={selectedCluster} user={user} />}
      />
      <AccordionComponent
        className={styles.accordion}
        headerClassName={styles.accordionHeader}
        heading={'Orchestrator Disks'}
        body={<OrchestratorDisks selectedCluster={selectedCluster} user={user} />}
      />
      <AccordionComponent
        className={styles.accordion}
        headerClassName={styles.accordionHeader}
        heading={'Orchestrator Database VM'}
        body={<OrchestratorDbVM selectedCluster={selectedCluster} user={user} />}
      />
    </VStack>
  )
}

export default Configs
