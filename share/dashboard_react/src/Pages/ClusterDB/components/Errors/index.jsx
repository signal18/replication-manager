import React from 'react'
import Logs from '../../../Dashboard/components/Logs'
import AccordionComponent from '../../../../components/AccordionComponent'
import styles from './styles.module.scss'

function Errors({ selectedDBServer }) {
  return (
    <AccordionComponent
      className={styles.accordion}
      heading={'Error logs'}
      allowToggle={false}
      body={<Logs logs={selectedDBServer?.errorLog?.buffer} className={styles.errorLogs} />}
    />
  )
}

export default Errors
