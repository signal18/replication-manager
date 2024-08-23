import { HStack, VStack } from '@chakra-ui/react'
import React, { useState, useEffect } from 'react'
import TagPill from '../../../components/TagPill'
import Dropdown from '../../../components/Dropdown'
import TableType2 from '../../../components/TableType2'
import parentStyles from '../styles.module.scss'
import { useDispatch, useSelector } from 'react-redux'
import { setSetting } from '../../../redux/settingsSlice'

function OrchestratorImages({ selectedCluster, user }) {
  const dispatch = useDispatch()
  const {
    cluster: { monitor }
  } = useSelector((state) => state)
  const [serviceRepos, setServiceRepos] = useState([])

  useEffect(() => {
    if (monitor?.serviceRepos?.length > 0) {
      setServiceRepos(monitor.serviceRepos)
    }
  }, [monitor?.serviceRepos])

  const dataObject = [
    {
      key: 'MariaDB',
      value: (
        <Dropdown
          className={parentStyles.dropdown}
          options={serviceRepos.find((repo) => repo.name === 'mariadb')?.tags?.results}
          selectedValue={selectedCluster?.config?.provDbDockerImg?.split(':')[1]}
          confirmTitle={`Confirm change database OCI image to mariadb:`}
          onChange={(value) => {
            dispatch(
              setSetting({
                clusterName: selectedCluster?.name,
                setting: 'prov-db-image',
                value: `mariadb:${value}`
              })
            )
          }}
        />
      )
    },
    {
      key: 'MySQL',
      value: (
        <Dropdown
          className={parentStyles.dropdown}
          options={serviceRepos.find((repo) => repo.name === 'mysql')?.tags?.results}
          selectedValue={selectedCluster?.config?.provDbDockerImg?.split(':')[1]}
          confirmTitle={`Confirm change database OCI image to mysql:`}
          onChange={(value) => {
            dispatch(
              setSetting({
                clusterName: selectedCluster?.name,
                setting: 'prov-db-image',
                value: `mysql:${value}`
              })
            )
          }}
        />
      )
    },
    {
      key: 'Percona',
      value: (
        <Dropdown
          className={parentStyles.dropdown}
          options={serviceRepos.find((repo) => repo.name === 'percona')?.tags?.results}
          selectedValue={selectedCluster?.config?.provDbDockerImg?.split(':')[1]}
          confirmTitle={`Confirm change database OCI image to percona:`}
          onChange={(value) => {
            dispatch(
              setSetting({
                clusterName: selectedCluster?.name,
                setting: 'prov-db-image',
                value: `percona:${value}`
              })
            )
          }}
        />
      )
    },
    {
      key: 'Postgress',
      value: (
        <Dropdown
          className={parentStyles.dropdown}
          options={serviceRepos.find((repo) => repo.name === 'postgres')?.tags?.results}
          selectedValue={selectedCluster?.config?.provDbDockerImg?.split(':')[1]}
          confirmTitle={`Confirm change database OCI image to postgres:`}
          onChange={(value) => {
            dispatch(
              setSetting({
                clusterName: selectedCluster?.name,
                setting: 'prov-db-image',
                value: `postgres:${value}`
              })
            )
          }}
        />
      )
    },
    {
      key: 'ProxySQL',
      value: (
        <Dropdown
          className={parentStyles.dropdown}
          options={serviceRepos.find((repo) => repo.name === 'myproxysqlsql')?.tags?.results}
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
          options={serviceRepos.find((repo) => repo.name === 'maxscale')?.tags?.results}
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
          options={serviceRepos.find((repo) => repo.name === 'haproxy')?.tags?.results}
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
          options={serviceRepos.find((repo) => repo.name === 'sphinx')?.tags?.results}
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
          options={serviceRepos.find((repo) => repo.name === 'mariadb')?.tags?.results}
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

export default OrchestratorImages
