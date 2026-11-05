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
  // the Databases and Proxies sub-tabs (level 3, #2268)
  const [dbTab, setDbTab] = useState(0)
  const [proxyTab, setProxyTab] = useState(0)

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
        options={['Databases', 'Proxies', 'Certificates']}
        tabIndex={tabIndex}
        onChange={setTabIndex}
        tabContents={[
          <TabItems
            key='db'
            variant='line'
            options={['System Resources', 'Available Fragments', 'Used Fragments', 'Config Pinning', 'Dynamic Config', 'Provision']}
            tabIndex={dbTab}
            onChange={setDbTab}
            tabContents={[
                <DBConfigs key='resources' selectedCluster={selectedCluster} user={user} part='resources' />,
                <DBConfigs key='available' selectedCluster={selectedCluster} user={user} part='available' />,
                <DBConfigs key='used' selectedCluster={selectedCluster} user={user} part='used' />,
                <DBConfigs key='pinning' selectedCluster={selectedCluster} user={user} part='pinning' />,
                <DBConfigs key='dynamic' selectedCluster={selectedCluster} user={user} part='dynamic' />,
                <VStack key='provision' className={styles.configContainer}>
                  <AccordionComponent className={styles.accordion} headerClassName={styles.accordionHeader} heading={'Orchestrator Images'} body={<OrchestratorImages selectedCluster={selectedCluster} user={user} scope='db' />} />
                  <AccordionComponent className={styles.accordion} headerClassName={styles.accordionHeader} heading={'Orchestrator Disks'} body={<OrchestratorDisks selectedCluster={selectedCluster} user={user} scope='db' />} />
                  <AccordionComponent className={styles.accordion} headerClassName={styles.accordionHeader} heading={'Orchestrator VM'} body={<OrchestratorDbVM selectedCluster={selectedCluster} user={user} scope='db' />} />
                </VStack>
            ]}
          />,
          <TabItems
            key='proxy'
            variant='line'
            options={['System Resources', 'Available Fragments', 'Used Fragments', 'Provision']}
            tabIndex={proxyTab}
            onChange={setProxyTab}
            tabContents={[
                <ProxyConfig key='resources' selectedCluster={selectedCluster} user={user} part='resources' />,
                <ProxyConfig key='available' selectedCluster={selectedCluster} user={user} part='available' />,
                <ProxyConfig key='used' selectedCluster={selectedCluster} user={user} part='used' />,
                <VStack key='provision' className={styles.configContainer}>
                  <AccordionComponent className={styles.accordion} headerClassName={styles.accordionHeader} heading={'Orchestrator Images'} body={<OrchestratorImages selectedCluster={selectedCluster} user={user} scope='proxy' />} />
                  <AccordionComponent className={styles.accordion} headerClassName={styles.accordionHeader} heading={'Orchestrator Disks'} body={<OrchestratorDisks selectedCluster={selectedCluster} user={user} scope='proxy' />} />
                  <AccordionComponent className={styles.accordion} headerClassName={styles.accordionHeader} heading={'Orchestrator VM'} body={<OrchestratorDbVM selectedCluster={selectedCluster} user={user} scope='proxy' />} />
                </VStack>
            ]}
          />,
          <VStack key='certificates' className={styles.configContainer}>
            <AccordionComponent className={styles.accordion} headerClassName={styles.accordionHeader} heading={'Certificates'} body={<Certificates selectedCluster={selectedCluster} user={user} />} />
          </VStack>
        ]}
      />
    </VStack>
  )
}

export default Configs
