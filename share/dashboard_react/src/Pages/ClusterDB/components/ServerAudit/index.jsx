import Logs from '../../../Dashboard/components/Logs'
import AccordionComponent from '../../../../components/AccordionComponent'
import styles from './styles.module.scss'
import { useDispatch, useSelector } from 'react-redux'
import { getDatabaseService } from '../../../../redux/clusterSlice'
import { useEffect } from 'react'

function ServerAudit({ clusterName, dbId, selectedDBServer }) {
  const dispatch = useDispatch()

  const auditLogs = useSelector((state) => state.cluster.database.auditLogs)

  useEffect(() => {
    dispatch(getDatabaseService({ clusterName, serviceName: 'auditlog', dbId }))
  }, [])

  return (
    <>
      <AccordionComponent
        className={styles.accordion}
        heading={'Server Audit logs'}
        allowToggle={true}
        body={<Logs key={"audit"} logs={auditLogs} className={styles.errorLogs} searchable={true} />}
      />
    </>
  )
}

export default ServerAudit
