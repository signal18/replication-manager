import React, { useEffect } from 'react'
import styles from './styles.module.scss'
import { Heading, VStack } from '@chakra-ui/react'
import TableType2 from '../../components/TableType2'
import RMTextarea from '../../components/RMTextarea'
import { useDispatch, useSelector } from 'react-redux'
import { getClusterCertificates } from '../../redux/clusterSlice'

function Certificates({ selectedCluster, user }) {
  const dispatch = useDispatch()

  const {
    cluster: { clusterCertificates }
  } = useSelector((state) => state)
  useEffect(() => {
    if (selectedCluster && clusterCertificates == null) {
      dispatch(getClusterCertificates({ clusterName: selectedCluster?.name }))
    }
  }, [selectedCluster])
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
  return (
    <VStack className={styles.certificateContainer}>
      <Heading className={styles.heading}>CLIENT CERTIFICATES</Heading>
      <TableType2 dataArray={dataObject} />
    </VStack>
  )
}

export default Certificates
