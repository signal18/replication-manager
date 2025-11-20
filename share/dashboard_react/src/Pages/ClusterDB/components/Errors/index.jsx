import Logs from '../../../Dashboard/components/Logs'
import AccordionComponent from '../../../../components/AccordionComponent'
import styles from './styles.module.scss'
import { useDispatch, useSelector } from 'react-redux'
import { getDatabaseService } from '../../../../redux/clusterSlice'
import { useEffect } from 'react'

function Errors({ clusterName, dbId, selectedDBServer }) {
  const dispatch = useDispatch()

  const errors = useSelector((state) => state.cluster.database.errors)
  const sqlerrors = useSelector((state) => state.cluster.database.sqlerrors)

  useEffect(() => {
    dispatch(getDatabaseService({ clusterName, serviceName: 'errorlog', dbId }))
    dispatch(getDatabaseService({ clusterName, serviceName: 'sqlerrorlog', dbId }))
  }, [])

  return (
    <>
      <AccordionComponent
        className={styles.accordion}
        heading={'Error logs'}
        allowToggle={true}
        body={<Logs key={"errors"} logs={errors} className={styles.errorLogs} searchable={true} />}
      />
      <AccordionComponent
        className={styles.accordion}
        heading={'SQL Error logs'}
        allowToggle={true}
        body={<Logs key={"sqlerrors"} logs={sqlerrors} className={styles.errorLogs} searchable={true} />}
      />
    </>
  )
}

export default Errors
