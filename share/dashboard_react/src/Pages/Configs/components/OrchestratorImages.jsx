import { Flex, HStack, VStack } from '@chakra-ui/react'
import React, { useState, useEffect } from 'react'
import TagPill from '../../../components/TagPill'
import Dropdown from '../../../components/Dropdown'
import TableType2 from '../../../components/TableType2'
import parentStyles from '../styles.module.scss'
import { useDispatch, useSelector } from 'react-redux'
import { setSetting } from '../../../redux/settingsSlice'
import RMButton from '../../../components/RMButton'

function OrchestratorImages({ selectedCluster, user }) {
  const dispatch = useDispatch()
  const {
    cluster: { monitor }
  } = useSelector((state) => state)
  const [serviceRepos, setServiceRepos] = useState([])
  const [versionOptions, setVersionOptions] = useState([])
  const [selectedDBType, setSelectedDBType] = useState(null)
  const [selectedDBVersion, setSelectedDBVersion] = useState(null)

  const [previousDBType, setPreviousDBType] = useState(null)
  const [previousDBVersion, setPreviousDBVersion] = useState(null)
  const [valueChanged, setValueChanged] = useState(false)
  const [dbTypes, setDbTypes] = useState([
    { name: 'MariaDB', value: 'mariadb' },
    { name: 'MySQL', value: 'mysql' },
    { name: 'Percona', value: 'percona' },
    { name: 'Postgress', value: 'postgres' }
  ])

  useEffect(() => {
    if (monitor?.serviceRepos?.length > 0) {
      setServiceRepos(monitor.serviceRepos)
      if (selectedCluster?.config?.provDbDockerImg) {
        const [dbType, dbVersion] = selectedCluster.config.provDbDockerImg.split(':')
        const selectedType = dbTypes.find((x) => x.value == dbType)
        setSelectedDBType(selectedType)
        setPreviousDBType(selectedType)
        const versions = monitor.serviceRepos.find((repo) => repo.name === selectedType.value)?.tags?.results
        const versionsWithValues = versions.map((x) => ({ name: x.name, value: x.name }))
        setVersionOptions(versionsWithValues)
        setSelectedDBVersion({ name: dbVersion, value: dbVersion })
        setPreviousDBVersion({ name: dbVersion, value: dbVersion })
      }
    }
  }, [monitor?.serviceRepos, selectedCluster?.config?.provDbDockerImg])

  const fillVersionDrodpown = (selectedType) => {
    setValueChanged(true)
    setSelectedDBType(selectedType)
    const versions = serviceRepos.find((repo) => repo.name === selectedType.value)?.tags?.results
    const versionsWithValues = versions.map((x) => ({ name: x.name, value: x.name }))
    setVersionOptions(versionsWithValues)
  }

  const handleSave = () => {
    setValueChanged(false)
    dispatch(
      setSetting({
        clusterName: selectedCluster?.name,
        setting: 'prov-db-image',
        value: `${selectedDBType.value}:${selectedDBVersion.value}`
      })
    )
  }

  const handleCancel = () => {
    setSelectedDBType(previousDBType)
    setSelectedDBVersion(previousDBVersion)
    setValueChanged(false)
  }

  const dataObject = [
    {
      key: 'MariaDB/MySQL/Percona/Postgress',
      value: (
        <Flex className={parentStyles.dbTypeVersion}>
          <Dropdown
            label='Type'
            selectedValue={selectedDBType?.value}
            className={parentStyles.dropdown}
            onChange={(value) => fillVersionDrodpown(value)}
            options={dbTypes}
          />
          <Dropdown
            label='Version'
            className={parentStyles.dropdown}
            options={versionOptions}
            selectedValue={selectedDBVersion?.value}
            // confirmTitle={`Confirm change database OCI image to ${selectedDBType.value}:`}
            onChange={(value) => {
              setValueChanged(true)
              setSelectedDBVersion(value)
            }}
          />

          {valueChanged && (
            <HStack justify='flex-start'>
              <RMButton variant='outline' colorScheme='white' onClick={handleCancel}>
                Cancel
              </RMButton>
              <RMButton onClick={handleSave}>Save</RMButton>
            </HStack>
          )}
        </Flex>
      )
    },
    {
      key: 'ProxySQL',
      value: (
        <Dropdown
          className={parentStyles.dropdown}
          options={serviceRepos
            .find((repo) => repo.name === 'proxysql')
            ?.tags?.results.map((x) => ({ name: x.name, value: x.name }))}
          selectedValue={selectedCluster?.config?.provProxyDockerProxysqlImg?.split(':')[1]}
          confirmTitle={`Confirm change database OCI image to proxysql:`}
          onChange={(value) => {
            dispatch(
              setSetting({
                clusterName: selectedCluster?.name,
                setting: 'prov-proxy-docker-proxysql-img',
                value: `proxysql:${value}`
              })
            )
          }}
        />
      )
    },
    {
      key: 'Maxscale',
      value: (
        <Dropdown
          className={parentStyles.dropdown}
          options={serviceRepos
            .find((repo) => repo.name === 'maxscale')
            ?.tags?.results.map((x) => ({ name: x.name, value: x.name }))}
          selectedValue={selectedCluster?.config?.provProxyDockerMaxscaleImg?.split(':')[1]}
          confirmTitle={`Confirm change database OCI image to maxscale:`}
          onChange={(value) => {
            dispatch(
              setSetting({
                clusterName: selectedCluster?.name,
                setting: 'prov-proxy-docker-maxscale-img',
                value: `maxscale:${value}`
              })
            )
          }}
        />
      )
    },
    {
      key: 'HaProxy',
      value: (
        <Dropdown
          className={parentStyles.dropdown}
          options={serviceRepos
            .find((repo) => repo.name === 'haproxy')
            ?.tags?.results.map((x) => ({ name: x.name, value: x.name }))}
          selectedValue={selectedCluster?.config?.provProxyDockerHaproxyImg?.split(':')[1]}
          confirmTitle={`Confirm change database OCI image to haproxy:`}
          onChange={(value) => {
            dispatch(
              setSetting({
                clusterName: selectedCluster?.name,
                setting: 'prov-db-image',
                value: `haproxy:${value}`
              })
            )
          }}
        />
      )
    },
    {
      key: 'Sphinx',
      value: (
        <Dropdown
          className={parentStyles.dropdown}
          options={serviceRepos
            .find((repo) => repo.name === 'sphinx')
            ?.tags?.results.map((x) => ({ name: x.name, value: x.name }))}
          selectedValue={selectedCluster?.config?.provSphinxDockerImg?.split(':')[1]}
          confirmTitle={`Confirm change database OCI image to sphinx:`}
          onChange={(value) => {
            dispatch(
              setSetting({
                clusterName: selectedCluster?.name,
                setting: 'setprov-sphinx-docker-img',
                value: `sphinx:${value}`
              })
            )
          }}
        />
      )
    },
    {
      key: 'ShardProxy',
      value: (
        <Dropdown
          className={parentStyles.dropdown}
          options={serviceRepos
            .find((repo) => repo.name === 'mariadb')
            ?.tags?.results.map((x) => ({ name: x.name, value: x.name }))}
          selectedValue={selectedCluster?.config?.provProxyDockerShardproxyImg?.split(':')[1]}
          confirmTitle={`Confirm change database OCI image to mariadb:`}
          onChange={(value) => {
            dispatch(
              setSetting({
                clusterName: selectedCluster?.name,
                setting: 'prov-proxy-docker-shardproxy-img',
                value: `mariadb:${value}`
              })
            )
          }}
        />
      )
    }
  ]
  return (
    <VStack className={parentStyles.orchContainer}>
      <HStack className={parentStyles.tags}>
        <TagPill colorScheme={'green'} text={selectedCluster?.config?.provDbDockerImg} />
        <TagPill colorScheme={'green'} text={selectedCluster?.config?.provProxyDockerProxysqlImg} />
        <TagPill colorScheme={'green'} text={selectedCluster?.config?.provProxyDockerMaxscaleImg} />
        <TagPill colorScheme={'green'} text={selectedCluster?.config?.provProxyDockerHaproxyImg} />
        <TagPill colorScheme={'green'} text={selectedCluster?.config?.provSphinxDockerImg} />
        <TagPill colorScheme={'green'} text={selectedCluster?.config?.provProxyDockerShardproxyImg} />
      </HStack>
      <TableType2 dataArray={dataObject} className={parentStyles.table} />
    </VStack>
  )
}

export default OrchestratorImages
