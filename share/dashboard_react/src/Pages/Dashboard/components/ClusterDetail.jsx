import React, { useState } from 'react'
import Card from '../../../components/Card'
import { Box, Flex, Text, Wrap } from '@chakra-ui/react'
import TagPill from '../../../components/TagPill'
import { useDispatch, useSelector } from 'react-redux'
import TableType2 from '../../../components/TableType2'
import ConfirmModal from '../../../components/Modals/ConfirmModal'
import {
  bootstrapMasterSlave,
  bootstrapMasterSlaveNoGtid,
  bootstrapMultiMaster,
  bootstrapMultiMasterRing,
  bootstrapMultiTierSlave,
  cancelRollingReprov,
  cancelRollingRestart,
  configDiscoverDB,
  configDynamic,
  configReload,
  failOverCluster,
  monitorAllSchemas,
  provisionCluster,
  reloadCertificates,
  resetFailOverCounter,
  resetSLA,
  rollingAction,
  rollingOptimize,
  rotateCertificates,
  rotateDBCredential,
  switchOverCluster,
  toggleTraffic,
  toggleTrafficStaging,
  unProvisionCluster
} from '../../../redux/clusterSlice'
import NewServerModal from '../../../components/Modals/NewServerModal'
import parentStyles from '../styles.module.scss'
import CopyTextModal from '../../../components/Modals/CopyTextModal'
import SetCredentialsModal from '../../../components/Modals/SetCredentialsModal'
import NewClusterModal from '../../../components/Modals/NewClusterModal'
import RMIconButton from '../../../components/RMIconButton'
import { HiCog } from 'react-icons/hi'
import { clusterService } from '../../../services/clusterService'

function ClusterDetail({ selectedCluster, user, readOnly = false, onOpenSettings }) {
  const dispatch = useDispatch()
  const isDesktop = useSelector((state) => state.common.isDesktop)
  const monitor = useSelector((state) => state.globalClusters.monitor)
  const clusterMaster = useSelector((state) => state.cluster.clusterMaster)
  const clusterServers = useSelector((state) => state.cluster.clusterServers)
  const clusterProxies = useSelector((state) => state.cluster.clusterProxies)
  const menuActionsLoading = useSelector((state) => state.cluster.loadingStates.menuActions)
  const rollingActionLoading = useSelector((state) => state.cluster.loadingStates.rollingAction)
  const baseURL = useSelector((state) => state?.auth?.baseURL)

  const [isConfirmModalOpen, setIsConfirmModalOpen] = useState(false)
  const [isNewServerModalOpen, setIsNewServerModalOpen] = useState(false)
  const [isNewClusterModalOpen, setIsNewClusterModalOpen] = useState(false)
  const [isCredentialModalOpen, setIsCredentialModalOpen] = useState(false)
  const [isClipboardModalOpen, setIsClipboardModalOpen] = useState(false)
  const [clipboardText, setClipboardText] = useState('')
  const [confirmHandler, setConfirmHandler] = useState(null)
  const [confirmTitle, setConfirmTitle] = useState('')
  const [confirmBody, setConfirmBody] = useState('')
  const [credentialType, setCredentialType] = useState('')
  const confirmBootrapMessage = 'Bootstrap operation will destroy your existing replication setup. \n Are you sure?'

  const openConfirmModal = () => {
    setConfirmBody('')
    setIsConfirmModalOpen(true)
  }
  const closeConfirmModal = () => {
    setIsConfirmModalOpen(false)
    setIsClipboardModalOpen(false)
    setConfirmHandler(null)
    setConfirmTitle('')
    setConfirmBody('')
    setClipboardText('')
  }

  const g = user?.grants ?? {}
  const isVisitor = !!user?.roles?.['visitor']

  const menuOptions = [
    {
      name: 'HA',
      subMenu: [
        {
          name: 'Reset Failover Counter',
          isDisabled: !g['cluster-settings'],
          onClick: () => {
            openConfirmModal()
            setConfirmTitle('Reset failover counter?')
            setConfirmHandler(() => () => dispatch(resetFailOverCounter({ clusterName: selectedCluster?.name })))
          }
        },
        {
          name: 'Rotate SLA',
          isDisabled: !g['cluster-reset-sla'],
          onClick: () => {
            openConfirmModal()
            setConfirmTitle('Reset SLA?')
            setConfirmHandler(() => () => dispatch(resetSLA({ clusterName: selectedCluster?.name })))
          }
        },
        {
          name: 'Toggle Traffic',
          isDisabled: !g['cluster-traffic'],
          onClick: () => {
            openConfirmModal()
            setConfirmTitle('Toggle traffic?')
            setConfirmHandler(() => () => dispatch(toggleTraffic({ clusterName: selectedCluster?.name })))
          }
        },
        ...(selectedCluster.config?.topologyStaging ? [{
          name: 'Toggle Traffic Staging',
          isDisabled: !g['cluster-traffic'],
          onClick: () => {
            openConfirmModal()
            setConfirmTitle('Toggle traffic staging?')
            setConfirmHandler(() => () => dispatch(toggleTrafficStaging({ clusterName: selectedCluster?.name })))
          }
        }] : []),
        ...(!isVisitor && !readOnly && clusterMaster?.state === 'Failed' && g['cluster-failover']
          ? [
            {
              name: 'Failover',
              onClick: () => {
                openConfirmModal()
                setConfirmTitle('Confirm failover?')
                setConfirmHandler(() => () => dispatch(failOverCluster({ clusterName: selectedCluster?.name })))
              }
            }
          ]
          : !isVisitor && !readOnly && clusterMaster?.state !== 'Failed' && g['cluster-switchover']
          ? [
            {
              name: 'Switchover',
              onClick: () => {
                openConfirmModal()
                setConfirmTitle('Confirm switchover?')
                setConfirmHandler(
                  () => () => dispatch(switchOverCluster({ clusterName: selectedCluster?.name }))
                )
              }
            }
          ]
          : [])
      ]
    },
    {
      name: 'Provision',
      subMenu: [
        {
          name: 'New Cluster Shard',
          isDisabled: !g['cluster-create'],
          onClick: () => {
            setIsNewClusterModalOpen(true)
          }
        },
        {
          name: 'New Monitor',
          isDisabled: !g['cluster-create-monitor'],
          onClick: () => {
            setIsNewServerModalOpen(true)
          }
        },
        {
          name: 'Provision Cluster',
          isDisabled: !g['prov-cluster'],
          onClick: () => {
            openConfirmModal()
            setConfirmTitle('Provision cluster?')
            setConfirmHandler(() => () => dispatch(provisionCluster({ clusterName: selectedCluster?.name })))
          }
        },
        {
          name: 'Unprovision Cluster',
          isDisabled: !g['prov-cluster-unprovision'],
          onClick: () => {
            openConfirmModal()
            setConfirmTitle('Unprovision cluster?')
            setConfirmHandler(() => () => dispatch(unProvisionCluster({ clusterName: selectedCluster?.name })))
          }
        }
      ]
    },
    {
      name: 'Credentials',
      subMenu: [
        {
          name: 'Set Database Credentials',
          isDisabled: !g['cluster-settings'],
          onClick: () => {
            setIsCredentialModalOpen(true)
            setCredentialType('db-servers-credential')
          }
        },
        {
          name: 'Set Replication Credentials',
          isDisabled: !g['cluster-settings'],
          onClick: () => {
            setIsCredentialModalOpen(true)
            setCredentialType('replication-credential')
          }
        },
        {
          name: 'Set DBA Credentials',
          isDisabled: !g['cluster-settings'],
          onClick: () => {
            setIsCredentialModalOpen(true)
            setCredentialType('cloud18-dba-user-credentials')
          }
        },
        {
          name: 'Set Sponsor DB Credentials',
          isDisabled: !g['cluster-settings'],
          onClick: () => {
            setIsCredentialModalOpen(true)
            setCredentialType('cloud18-sponsor-user-credentials')
          }
        },
        {
          name: 'Set ProxySQL Credentials',
          isDisabled: !g['cluster-settings'],
          onClick: () => {
            setIsCredentialModalOpen(true)
            setCredentialType('proxysql-servers-credential')
          }
        },
        {
          name: 'Set Maxscale Credentials',
          isDisabled: !g['cluster-settings'],
          onClick: () => {
            setIsCredentialModalOpen(true)
            setCredentialType('maxscale-servers-credential')
          }
        },
        {
          name: 'Set Sharding Proxy Credentials',
          isDisabled: !g['cluster-settings'],
          onClick: () => {
            setIsCredentialModalOpen(true)
            setCredentialType('shardproxy-servers-credential')
          }
        },
        {
          name: 'Rotate Database Credentials',
          isDisabled: !g['cluster-rotate-passwords'],
          onClick: () => {
            openConfirmModal()
            setConfirmTitle('Rotate database credentials?')
            setConfirmHandler(() => () => dispatch(rotateDBCredential({ clusterName: selectedCluster?.name })))
          }
        }
      ]
    },
    {
      name: 'Maintenance',
      subMenu: [
        {
          name: 'Run Monitor Schema',
          isDisabled: !selectedCluster?.config?.monitoringSchemaChange || !g['cluster-sharding'],
          onClick: () => {
            openConfirmModal()
            setConfirmTitle('Run monitor schema for all databases?')
            setConfirmHandler(() => () => dispatch(monitorAllSchemas({ clusterName: selectedCluster?.name })))
          }
        },
        {
          name: 'Rolling Optimize',
          isDisabled: !g['cluster-rolling'],
          onClick: () => {
            openConfirmModal()
            setConfirmTitle('Rolling optimize?')
            setConfirmHandler(() => () => dispatch(rollingOptimize({ clusterName: selectedCluster?.name })))
          }
        },
        {
          name: 'Rolling Jobs Upgrade',
          isDisabled: !g['cluster-rolling'] || rollingActionLoading,
          onClick: () => {
            openConfirmModal()
            setConfirmTitle('Rolling jobs upgrade?')
            setConfirmHandler(() => () => dispatch(rollingAction({ clusterName: selectedCluster?.name, action: 'jobs-upgrade' })))
          }
        },
        {
          name: 'Rolling Restart',
          isDisabled: !g['cluster-rolling'] || rollingActionLoading,
          onClick: () => {
            openConfirmModal()
            setConfirmTitle('Rolling restart?')
            setConfirmHandler(() => () => dispatch(rollingAction({ clusterName: selectedCluster?.name, action: 'restart' })))
          }
        },
        {
          name: 'Rolling Reprov',
          isDisabled: !g['cluster-rolling'] || rollingActionLoading,
          onClick: () => {
            openConfirmModal()
            setConfirmTitle('Rolling reprovision?')
            setConfirmBody('Each server will be unprovisioned then re-provisioned one by one, starting with replicas before the primary. The cluster remains available throughout.')
            setConfirmHandler(() => () => dispatch(rollingAction({ clusterName: selectedCluster?.name, action: 'reprov' })))
          }
        },
        {
          name: 'Rolling Upgrade',
          isDisabled: !g['cluster-rolling'] || rollingActionLoading,
          onClick: () => {
            openConfirmModal()
            setConfirmTitle('Rolling upgrade?')
            setConfirmHandler(() => () => dispatch(rollingAction({ clusterName: selectedCluster?.name, action: 'upgrade' })))
          }
        },
        {
          name: 'Rotate Certificates',
          isDisabled: !g['cluster-certificates-rotate'],
          onClick: () => {
            openConfirmModal()
            setConfirmTitle('Rotate certificates?')
            setConfirmHandler(() => () => dispatch(rotateCertificates({ clusterName: selectedCluster?.name })))
          }
        },
        {
          name: 'Reload Certificates',
          isDisabled: !g['cluster-certificates-reload'],
          onClick: () => {
            openConfirmModal()
            setConfirmTitle('Reload certificates?')
            setConfirmHandler(() => () => dispatch(reloadCertificates({ clusterName: selectedCluster?.name })))
          }
        },
        {
          name: 'Cancel Rolling Restart',
          isDisabled: !g['cluster-rolling'],
          onClick: () => {
            openConfirmModal()
            setConfirmTitle('Cancel Rolling Restart?')
            setConfirmHandler(() => () => dispatch(cancelRollingRestart({ clusterName: selectedCluster?.name })))
          }
        },
        {
          name: 'Cancel Rolling Reprove',
          isDisabled: !g['cluster-rolling'],
          onClick: () => {
            openConfirmModal()
            setConfirmTitle('Cancel Rolling Reprove?')
            setConfirmHandler(() => () => dispatch(cancelRollingReprov({ clusterName: selectedCluster?.name })))
          }
        }
      ]
    },
    {
      name: 'Replication Bootstrap',
      subMenu: [
        {
          name: 'Master Slave',
          isDisabled: !g['cluster-replication'],
          onClick: () => {
            openConfirmModal()
            setConfirmTitle(confirmBootrapMessage)
            setConfirmHandler(() => () => dispatch(bootstrapMasterSlave({ clusterName: selectedCluster?.name })))
          }
        },
        {
          name: 'Master Slave Positional',
          isDisabled: !g['cluster-replication'],
          onClick: () => {
            openConfirmModal()
            setConfirmTitle(confirmBootrapMessage)
            setConfirmHandler(() => () => dispatch(bootstrapMasterSlaveNoGtid({ clusterName: selectedCluster?.name })))
          }
        },
        {
          name: 'Multi Master',
          isDisabled: !g['cluster-replication'],
          onClick: () => {
            openConfirmModal()
            setConfirmTitle(confirmBootrapMessage)
            setConfirmHandler(() => () => dispatch(bootstrapMultiMaster({ clusterName: selectedCluster?.name })))
          }
        },
        {
          name: 'Multi Master Ring',
          isDisabled: !g['cluster-replication'],
          onClick: () => {
            openConfirmModal()
            setConfirmTitle(confirmBootrapMessage)
            setConfirmHandler(() => () => dispatch(bootstrapMultiMasterRing({ clusterName: selectedCluster?.name })))
          }
        },
        {
          name: 'Multi Tier Slave',
          isDisabled: !g['cluster-replication'],
          onClick: () => {
            openConfirmModal()
            setConfirmTitle(confirmBootrapMessage)
            setConfirmHandler(() => () => dispatch(bootstrapMultiTierSlave({ clusterName: selectedCluster?.name })))
          }
        }
      ]
    },
    {
      name: 'Config',
      subMenu: [
        {
          name: 'Reload',
          isDisabled: !g['cluster-settings'],
          onClick: () => {
            openConfirmModal()
            setConfirmTitle('Confirm reload config?')
            setConfirmHandler(() => () => dispatch(configReload({ clusterName: selectedCluster?.name })))
          }
        },
        {
          name: 'Database discover config',
          isDisabled: !g['cluster-settings'],
          onClick: () => {
            openConfirmModal()
            setConfirmTitle('Confirm database discover config?')
            setConfirmHandler(() => () => dispatch(configDiscoverDB({ clusterName: selectedCluster?.name })))
          }
        },
        {
          name: 'Database apply dynamic config',
          isDisabled: !g['db-config-flag'],
          onClick: () => {
            openConfirmModal()
            setConfirmTitle('Confirm database apply config?')
            setConfirmHandler(() => () => dispatch(configDynamic({ clusterName: selectedCluster?.name })))
          }
        }
      ]
    },
    {
      name: 'Debug',
      subMenu: [
        {
          name: 'Clusters',
          isDisabled: !g['cluster-debug'],
          onClick: () => {
            setIsClipboardModalOpen(true)
            setClipboardText(JSON.stringify(selectedCluster))
            setConfirmTitle('Json of selected cluster')
          }
        },
        {
          name: 'Servers',
          isDisabled: !g['cluster-debug'],
          onClick: () => {
            setIsClipboardModalOpen(true)
            setClipboardText(JSON.stringify(clusterServers))
            setConfirmTitle('Json of database servers')
          }
        },
        {
          name: 'Proxies',
          isDisabled: !g['cluster-debug'],
          onClick: () => {
            setIsClipboardModalOpen(true)
            setClipboardText(JSON.stringify(clusterProxies))
            setConfirmTitle('Json of proxy servers')
          }
        }
      ]
    }
  ]

  const dataObject = [
    { key: 'Name', value: selectedCluster?.name },
    { key: 'Orchestrator', value: selectedCluster?.config?.provOrchestrator },
    {
      key: 'Status',
      value: (
        <Wrap>
          {
            <>
              {selectedCluster?.config?.testInjectTraffic && <TagPill type='success' text='PrxTraffic' />}
              {selectedCluster?.config?.testInjectTrafficStaging && <TagPill type='success' text='PrxTrafficStaging' />}
              {selectedCluster?.config?.monitoringPause && <TagPill colorScheme='red' isBlinking={true} text='NotMonitored' />}
              {selectedCluster?.isProvision ? <TagPill colorScheme='green' text='IsProvision' /> : <TagPill colorScheme='orange' text='NeedProvision' />}
              {selectedCluster?.isNeedDatabasesRollingRestart && <TagPill colorScheme='orange' text='NeedRollingRestart' />}
              {selectedCluster?.isNeedDatabasesRollingReprov && <TagPill colorScheme='orange' text='NeedRollingReprov' />}
              {selectedCluster?.isNeedDatabasesRestart && <TagPill colorScheme='orange' text='NeedDabaseRestart' />}
              {selectedCluster?.isNeedDatabasesReprov && <TagPill colorScheme='orange' text='NeedDatabaseReprov' />}
              {selectedCluster?.isNeedProxiesRestart && <TagPill colorScheme='orange' text='NeedProxyRestart' />}
              {selectedCluster?.isNeedProxiesReprov && <TagPill colorScheme='orange' text='NeedProxyReprov' />}
              {selectedCluster?.isConfigPathChange && <TagPill colorScheme='orange' text='DBConfigPathChanged' />}
              {selectedCluster?.isNotMonitoring && <TagPill colorScheme='orange' text='UnMonitored' />}
              {selectedCluster?.isCapturing && <TagPill colorScheme='orange' text='Capturing' />}
              {selectedCluster?.isNeedAppsReprov && <TagPill colorScheme='orange' text='NeedAppsReprov' />}
            </>
          }
        </Wrap>
      )
    }
  ]

  return (
    <>
      <Card
        width={isDesktop ? '50%' : '100%'}
        header={
          <>
            <Text>Cluster</Text>
            <Box ml='auto' display='flex' gap={1} alignItems='center'>
              {selectedCluster?.activePassiveStatus === 'A' ? (
                <TagPill colorScheme='green' text={'Active'} />
              ) : selectedCluster?.activePassiveStatus === 'S' ? (
                <TagPill colorScheme='orange' text={'Standby'} />
              ) : null}
              {selectedCluster?.config?.arbitrationExternal && (
                <TagPill
                  colorScheme={selectedCluster?.isFailedArbitrator ? 'red' : 'green'}
                  text='ARB'
                />
              )}
            </Box>
          </>
        }
        body={
          <TableType2
            dataArray={dataObject}
            className={`${parentStyles.table} ${parentStyles.clusterDetailTable}`}
            labelClassName={`${parentStyles.rowLabel} ${parentStyles.ClusterDetailRow}`}
            valueClassName={`${parentStyles.rowValue} ${parentStyles.ClusterDetailRow}`}
          />
        }
        headerAction='menu'
        isLoading={menuActionsLoading}
        menuOptions={menuOptions}
        extraHeaderActions={onOpenSettings ? <RMIconButton icon={HiCog} tooltip='Topology Settings' onClick={onOpenSettings} size='xs' variant='ghost' /> : null}
      />
      {isConfirmModalOpen && (
        <ConfirmModal
          isOpen={isConfirmModalOpen}
          closeModal={closeConfirmModal}
          title={confirmTitle}
          body={confirmBody || undefined}
          onConfirmClick={() => {
            confirmHandler()
            closeConfirmModal()
          }}
        />
      )}

      {isClipboardModalOpen && (
        <CopyTextModal
          isOpen={isClipboardModalOpen}
          text={clipboardText}
          closeModal={closeConfirmModal}
          title={confirmTitle}
          showPrettyJsonCheckbox={true}
        />
      )}

      {isNewClusterModalOpen && (
        <NewClusterModal 
        plans={monitor?.servicePlans} 
        orchestrators={monitor?.serviceOrchestrators} 
        defaultOrchestrator={monitor?.config.provOrchestrator} 
        isOpen={isNewClusterModalOpen} 
        clusterHead={selectedCluster?.name}
        closeModal={() => setIsNewClusterModalOpen(false)} />
      )}

      {isNewServerModalOpen && (
        <NewServerModal
          clusterName={selectedCluster?.name}
          isOpen={isNewServerModalOpen}
          closeModal={() => setIsNewServerModalOpen(false)}
        />
      )}
      {isCredentialModalOpen && (
        <SetCredentialsModal
          clusterName={selectedCluster?.name}
          isOpen={isCredentialModalOpen}
          type={credentialType}
          closeModal={() => {
            setIsCredentialModalOpen(false)
            setCredentialType('')
          }}
        />
      )}
    </>
  )
}

export default ClusterDetail
