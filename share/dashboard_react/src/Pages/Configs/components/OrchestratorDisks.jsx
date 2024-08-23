import { VStack } from '@chakra-ui/react'
import React, { useState, useEffect } from 'react'
import Dropdown from '../../../components/Dropdown'
import TableType2 from '../../../components/TableType2'
import parentStyles from '../styles.module.scss'
import { useDispatch, useSelector } from 'react-redux'
import { setSetting } from '../../../redux/settingsSlice'
import { convertObjectToArray } from '../../../utility/common'
import TextForm from '../../../components/TextForm'

function OrchestratorDisks({ selectedCluster, user }) {
  const dispatch = useDispatch()
  const {
    cluster: { monitor }
  } = useSelector((state) => state)
  const [serviceDisks, setServiceDisks] = useState([])
  const [serviceFS, setServiceFS] = useState([])
  const [servicePool, setServicePool] = useState([])

  useEffect(() => {
    if (monitor?.serviceDisk) {
      setServiceDisks(convertObjectToArray(monitor.serviceDisk))
    }
    if (monitor?.serviceFS) {
      setServiceFS(convertObjectToArray(monitor.serviceFS))
    }
    if (monitor?.servicePool) {
      setServicePool(convertObjectToArray(monitor.servicePool))
    }
  }, [monitor?.serviceDisk, monitor?.serviceFS, monitor?.servicePool])

  const dataObject = [
    {
      key: 'Database',
      value: [
        {
          key: 'Database Disk Type',
          value: (
            <Dropdown
              className={parentStyles.dropdown}
              options={serviceDisks}
              selectedValue={selectedCluster?.config?.provDbDiskType}
              confirmTitle={`Confirm change DB disk type to `}
              onChange={(value) => {
                dispatch(
                  setSetting({
                    clusterName: selectedCluster?.name,
                    setting: 'prov-db-disk-type',
                    value: value
                  })
                )
              }}
            />
          )
        },
        {
          key: 'Database Disk FS',
          value: (
            <Dropdown
              className={parentStyles.dropdown}
              options={serviceFS}
              selectedValue={selectedCluster?.config?.provDbDiskFs}
              confirmTitle={`Confirm change DB disk FS to `}
              onChange={(value) => {
                dispatch(
                  setSetting({
                    clusterName: selectedCluster?.name,
                    setting: 'prov-db-disk-fs',
                    value: value
                  })
                )
              }}
            />
          )
        },
        {
          key: 'Database Disk Pool',
          value: (
            <Dropdown
              className={parentStyles.dropdown}
              options={servicePool}
              selectedValue={selectedCluster?.config?.provDbDiskPool}
              confirmTitle={`Confirm change DB disk pool to `}
              onChange={(value) => {
                dispatch(
                  setSetting({
                    clusterName: selectedCluster?.name,
                    setting: 'prov-db-disk-pool',
                    value: value
                  })
                )
              }}
            />
          )
        },
        {
          key: 'Name',
          value: (
            <TextForm
              value={selectedCluster?.config?.provDbDiskDevice}
              confirmTitle={`Confirm change onfirm change DB disk device name to `}
              className={parentStyles.textbox}
              onSave={(value) =>
                dispatch(
                  setSetting({
                    clusterName: selectedCluster?.name,
                    setting: 'prov-db-disk-device',
                    value: value
                  })
                )
              }
            />
          )
        }
      ]
    },
    {
      key: 'Proxy',
      value: [
        {
          key: 'Proxy Disk Type',
          value: (
            <Dropdown
              className={parentStyles.dropdown}
              options={serviceDisks}
              selectedValue={selectedCluster?.config?.provProxyDiskType}
              confirmTitle={`Confirm change proxy disk type to `}
              onChange={(value) => {
                dispatch(
                  setSetting({
                    clusterName: selectedCluster?.name,
                    setting: 'prov-proxy-disk-type',
                    value: value
                  })
                )
              }}
            />
          )
        },
        {
          key: 'Proxy Disk FS',
          value: (
            <Dropdown
              className={parentStyles.dropdown}
              options={serviceFS}
              selectedValue={selectedCluster?.config?.provProxyDiskFs}
              confirmTitle={`Confirm change proxy disk FS to `}
              onChange={(value) => {
                dispatch(
                  setSetting({
                    clusterName: selectedCluster?.name,
                    setting: 'prov-proxy-disk-fs',
                    value: value
                  })
                )
              }}
            />
          )
        },
        {
          key: 'Proxy Disk Pool',
          value: (
            <Dropdown
              className={parentStyles.dropdown}
              options={servicePool}
              selectedValue={selectedCluster?.config?.provProxyDiskPool}
              confirmTitle={`Confirm change proxy disk pool to `}
              onChange={(value) => {
                dispatch(
                  setSetting({
                    clusterName: selectedCluster?.name,
                    setting: 'prov-proxy-disk-pool',
                    value: value
                  })
                )
              }}
            />
          )
        },
        {
          key: 'Name',
          value: (
            <TextForm
              value={selectedCluster?.config?.provProxyDiskDevice}
              confirmTitle={`Confirm change onfirm change proxy disk device name to `}
              className={parentStyles.textbox}
              onSave={(value) =>
                dispatch(
                  setSetting({
                    clusterName: selectedCluster?.name,
                    setting: 'prov-proxy-disk-device',
                    value: value
                  })
                )
              }
            />
          )
        }
      ]
    }
  ]
  return (
    <VStack>
      <TableType2
        dataArray={dataObject}
        className={parentStyles.table}
        labelClassName={parentStyles.label}
        valueClassName={parentStyles.value}
        rowDivider={true}
        rowClassName={parentStyles.row}
      />
    </VStack>
  )
}

export default OrchestratorDisks
