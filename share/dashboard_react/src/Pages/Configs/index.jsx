import React, { useEffect } from 'react'
import AccordionComponent from '../../components/AccordionComponent'
import styles from './styles.module.scss'
import DBConfigs from './components/DBConfigs'
import ProxyConfig from './components/ProxyConfig'
import { HStack, Text, VStack } from '@chakra-ui/react'
import OrchestratorImages from './components/OrchestratorImages'
import OrchestratorDisks from './components/OrchestratorDisks'
import OrchestratorDbVM from './components/OrchestratorDbVM'
import Certificates from './components/Certificates'
import { useDispatch, useSelector } from 'react-redux'
import { getClusterCertificates } from '../../redux/clusterSlice'
import { switchSetting } from '../../redux/settingsSlice'
import RMSwitch from '../../components/RMSwitch'

function Configs({ selectedCluster, user }) {
  const dispatch = useDispatch()

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
      <HStack px={4} py={2} spacing={3}>
        <Text fontWeight={600} fontSize='sm'>Enable Configurator</Text>
        <RMSwitch
          isChecked={selectedCluster?.config?.provDbConfig}
          isDisabled={user?.grants['cluster-settings'] == false}
          confirmTitle={'Confirm switch settings for prov-db-config? When disabled, no config is deployed to databases but tracking remains visible.'}
          onChange={() =>
            dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'prov-db-config' }))
          }
        />
      </HStack>
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
      <AccordionComponent
        className={styles.accordion}
        headerClassName={styles.accordionHeader}
        heading={'Certificates'}
        body={<Certificates selectedCluster={selectedCluster} user={user} />}
      />
    </VStack>
  )
}

export default Configs
