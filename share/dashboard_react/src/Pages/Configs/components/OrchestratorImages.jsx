import { Flex, HStack, VStack } from '@chakra-ui/react'
import React, { useState, useEffect } from 'react'
import TagPill from '../../../components/TagPill'
import Dropdown from '../../../components/Dropdown'
import TableType2 from '../../../components/TableType2'
import parentStyles from '../styles.module.scss'
import { useDispatch, useSelector } from 'react-redux'
import { setSetting } from '../../../redux/settingsSlice'
import RMButton from '../../../components/RMButton'

function OrchestratorImages({ selectedCluster }) {
  const dispatch = useDispatch()
  const {
    globalClusters: { monitor }
  } = useSelector((state) => state)
  const [serviceRepos, setServiceRepos] = useState([])
  const [versionOptions, setVersionOptions] = useState([])
  const [selectedDBType, setSelectedDBType] = useState(null)
  const [selectedDBVersion, setSelectedDBVersion] = useState(null)

  const [previousDBType, setPreviousDBType] = useState(null)
  const [previousDBVersion, setPreviousDBVersion] = useState(null)
  const [valueChanged, setValueChanged] = useState(false)
  const dbTypes = [
    { name: 'MariaDB', value: 'mariadb' },
    { name: 'MySQL', value: 'mysql' },
    { name: 'Percona', value: 'percona' },
    { name: 'Postgress', value: 'postgres' }
  ]

  useEffect(() => {
    if (monitor?.serviceRepos?.length > 0) {
      const repos = monitor?.serviceRepos.map(entry => ({
        name: entry.name,
        image: entry.image,
        options: entry.tags?.results?.map(tag => ({
          name: tag.name,
          value: `${entry.image}:${tag.name}`
        }))
      }))

      setServiceRepos(repos)
    }
  }, [monitor?.serviceRepos])

  useEffect(() => {
    if (selectedCluster?.config?.provDbDockerImg) {
      const dbType = selectedCluster.config.provDbDockerImg.split(':')[0]
      const repo = serviceRepos.find((r) => r.image === dbType)
      setSelectedDBType(repo?.name)
      setPreviousDBType(repo?.name)
      setVersionOptions(repo?.options || [])
      setSelectedDBVersion(selectedCluster.config.provDbDockerImg)
      setPreviousDBVersion(selectedCluster.config.provDbDockerImg)
    }
  }, [serviceRepos, selectedCluster?.config?.provDbDockerImg])

  const fillVersionDropdown = (selectedType) => {
    setValueChanged(true)
    setSelectedDBType(selectedType)
    const repo = serviceRepos.find((r) => r.name === selectedType)
    setVersionOptions(repo?.options || [])
  }

  const handleSave = () => {
    setValueChanged(false)
    dispatch(
      setSetting({
        clusterName: selectedCluster?.name,
        setting: 'prov-db-image',
        value: `${selectedDBVersion}`
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
            selectedValue={selectedDBType}
            className={parentStyles.dropdown}
            onChange={(opt) => fillVersionDropdown(opt.value)}
            options={dbTypes}
          />
          <Dropdown
            label='Version'
            className={parentStyles.dropdown}
            options={versionOptions}
            selectedValue={selectedDBVersion}
            onChange={(opt) => {
              setValueChanged(true)
              setSelectedDBVersion(opt.value)
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
          options={serviceRepos.find((repo) => repo.name === 'proxysql')?.options || []}
          selectedValue={selectedCluster?.config?.provProxyDockerProxysqlImg}
          confirmTitle={`Confirm change database OCI image to proxysql:`}
          onChange={(value) => {
            dispatch(
              setSetting({
                clusterName: selectedCluster?.name,
                setting: 'prov-proxy-docker-proxysql-img',
                value: `${value}`
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
          options={serviceRepos.find((repo) => repo.name === 'maxscale')?.options || []}
          selectedValue={selectedCluster?.config?.provProxyDockerMaxscaleImg}
          confirmTitle={`Confirm change database OCI image to maxscale:`}
          onChange={(value) => {
            dispatch(
              setSetting({
                clusterName: selectedCluster?.name,
                setting: 'prov-proxy-docker-maxscale-img',
                value: `${value}`
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
          options={serviceRepos.find((repo) => repo.name === 'haproxy')?.options || []}
          selectedValue={selectedCluster?.config?.provProxyDockerHaproxyImg}
          confirmTitle={`Confirm change database OCI image to haproxy:`}
          onChange={(value) => {
            dispatch(
              setSetting({
                clusterName: selectedCluster?.name,
                setting: 'prov-proxy-docker-haproxy-img',
                value: `${value}`
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
          options={serviceRepos.find((repo) => repo.name === 'sphinx')?.options || []}
          selectedValue={selectedCluster?.config?.provSphinxDockerImg}
          confirmTitle={`Confirm change database OCI image to sphinx:`}
          onChange={(value) => {
            dispatch(
              setSetting({
                clusterName: selectedCluster?.name,
                setting: 'prov-sphinx-img',
                value: `${value}`
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
          options={serviceRepos.find((repo) => repo.name === 'mariadb')?.tags?.options || []}
          selectedValue={selectedCluster?.config?.provProxyDockerShardproxyImg}
          confirmTitle={`Confirm change database OCI image to mariadb:`}
          onChange={(value) => {
            dispatch(
              setSetting({
                clusterName: selectedCluster?.name,
                setting: 'prov-proxy-docker-shardproxy-img',
                value: `${value}`
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
