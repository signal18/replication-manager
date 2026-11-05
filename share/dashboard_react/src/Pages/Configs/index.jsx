import React, { useEffect, useState } from 'react'
import AccordionComponent from '../../components/AccordionComponent'
import TabItems from '../../components/TabItems'
import styles from './styles.module.scss'
import DBConfigs from './components/DBConfigs'
import ProxyConfig from './components/ProxyConfig'
import { VStack } from '@chakra-ui/react'
import OrchestratorImages from './components/OrchestratorImages'
import OrchestratorDisks from './components/OrchestratorDisks'
import OrchestratorDbVM from './components/OrchestratorDbVM'
import Certificates from './components/Certificates'
import { useDispatch, useSelector } from 'react-redux'
import { getClusterCertificates } from '../../redux/clusterSlice'

function Configs({ selectedCluster, user }) {
  const dispatch = useDispatch()
  const [tabIndex, setTabIndex] = useState(0)

  const {
    cluster: { clusterCertificates }
  } = useSelector((state) => state)

  useEffect(() => {
    if (selectedCluster && clusterCertificates == null) {
      dispatch(getClusterCertificates({ clusterName: selectedCluster?.name }))
    }
  }, [selectedCluster, clusterCertificates])

  return (
    <VStack className={styles.configContainer}>
      <TabItems
        options={['Databases', 'Proxies']}
        tabIndex={tabIndex}
        onChange={setTabIndex}
        tabContents={[
          <VStack key='db' className={styles.configContainer}>
            <AccordionComponent
              className={styles.accordion}
              headerClassName={styles.accordionHeader}
              heading={'System Resources'}
              body={<DBConfigs selectedCluster={selectedCluster} user={user} />}
            />
            <AccordionComponent
              className={styles.accordion}
              headerClassName={styles.accordionHeader}
              heading={'Orchestrator Images'}
              body={<OrchestratorImages selectedCluster={selectedCluster} user={user} scope='db' />}
            />
            <AccordionComponent
              className={styles.accordion}
              headerClassName={styles.accordionHeader}
              heading={'Orchestrator Disks'}
              body={<OrchestratorDisks selectedCluster={selectedCluster} user={user} scope='db' />}
            />
            <AccordionComponent
              className={styles.accordion}
              headerClassName={styles.accordionHeader}
              heading={'Orchestrator Database VM'}
              body={<OrchestratorDbVM selectedCluster={selectedCluster} user={user} scope='db' />}
            />
            <AccordionComponent
              className={styles.accordion}
              headerClassName={styles.accordionHeader}
              heading={'Certificates'}
              body={<Certificates selectedCluster={selectedCluster} user={user} />}
            />
              \n          </VStack>,
          <VStack key='proxy' className={styles.configContainer}>
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
              body={<OrchestratorImages selectedCluster={selectedCluster} user={user} scope='proxy' />}
            />
            <AccordionComponent
              className={styles.accordion}
              headerClassName={styles.accordionHeader}
              heading={'Orchestrator Disks'}
              body={<OrchestratorDisks selectedCluster={selectedCluster} user={user} scope='proxy' />}
            />
            <AccordionComponent
              className={styles.accordion}
              headerClassName={styles.accordionHeader}
              heading={'Orchestrator Proxy VM'}
              body={<OrchestratorDbVM selectedCluster={selectedCluster} user={user} scope='proxy' />}
            />
              \n          </VStack>
              ]}
            />
    </VStack>
  )
}

export default Configs
