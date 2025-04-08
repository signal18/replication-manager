import React, { useEffect, useState } from 'react'
import TableType2 from '../../../components/TableType2'
import { useDispatch, useSelector } from 'react-redux'
import RMTextarea from '../../../components/RMTextarea'
import parentStyles from '../styles.module.scss'
import { setSetting } from '../../../redux/settingsSlice'
import { convertObjectToArrayForDropdown } from '../../../utility/common'
import Dropdown from '../../../components/Dropdown'
import { Stack, Text } from '@chakra-ui/react'

function Certificates({ selectedCluster, user }) {
  const {
    cluster: { clusterCertificates },
    globalClusters: { monitor }
  } = useSelector((state) => state)

  const dispatch = useDispatch()
  const [serviceSSLMode, setServiceSSLMode] = useState([])

  useEffect(() => {
    if (monitor?.serviceSslMode) {
      setServiceSSLMode([{ label: "Default", value: "" }, ...convertObjectToArrayForDropdown(monitor.serviceSslMode)])
    }
  }, [monitor?.serviceSslMode])

  const handleCaCertChange = () => { }
  const handleClientCertChange = () => { }
  const handleClienKeyChange = () => { }
  const dataObject = [
    {
      key: <Stack>
        <Text as="span">Client SSL Mode</Text>
        <Text as="span" fontSize='sm' color='gray.500'>(Default (empty) will try to use SSL and use VERIFY_CA if SSL Tag is activated in configurator. Choose manually to override)</Text>
      </Stack>,
      value: (
        <Dropdown
          className={parentStyles.dropdown}
          options={serviceSSLMode}
          selectedValue={selectedCluster?.config?.dbServersTlsSslMode}
          confirmTitle={`Confirm change DB SSL Mode to `}
          placeholder='SSL Mode'
          onChange={(value) => {
            dispatch(
              setSetting({
                clusterName: selectedCluster?.name,
                setting: 'db-servers-tls-ssl-mode',
                value: value === undefined ? '' : value
              })
            )
          }}
        />
      )
    },
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
