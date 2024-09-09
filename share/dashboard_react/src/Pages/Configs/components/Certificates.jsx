import React from 'react'
import TableType2 from '../../../components/TableType2'
import { useSelector } from 'react-redux'
import RMTextarea from '../../../components/RMTextarea'
import parentStyles from '../styles.module.scss'

function Certificates(props) {
  const {
    cluster: { clusterCertificates }
  } = useSelector((state) => state)

  const handleCaCertChange = () => {}
  const handleClientCertChange = () => {}
  const handleClienKeyChange = () => {}
  const dataObject = [
    {
      key: 'Ca Cert',
      value: <RMTextarea value={clusterCertificates?.caCert} readOnly={true} handleInputChange={handleCaCertChange} />
    },
    {
      key: 'Client Cert',
      value: (
        <RMTextarea
          value={clusterCertificates?.clientCert}
          readOnly={true}
          handleInputChange={handleClientCertChange}
        />
      )
    },
    {
      key: 'Client Key',
      value: (
        <RMTextarea value={clusterCertificates?.clientKey} readOnly={true} handleInputChange={handleClienKeyChange} />
      )
    }
  ]
  return <TableType2 dataArray={dataObject} className={parentStyles.table} />
}

export default Certificates
