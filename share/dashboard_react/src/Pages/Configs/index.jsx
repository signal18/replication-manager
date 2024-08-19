import React from 'react'
import AccordionComponent from '../../components/AccordionComponent'
import DBConfigs from './components/DBConfigs'
import styles from './styles.module.scss'

function Configs({ selectedCluster, user }) {
  return (
    <AccordionComponent
      headerClassName={styles.accordionHeader}
      heading={'Database Configurator'}
      body={<DBConfigs selectedCluster={selectedCluster} user={user} />}
    />
  )
}

export default Configs
