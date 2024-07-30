import React, { useState } from 'react'
import Card from '../../../components/Card'
import { Box, Text, Wrap } from '@chakra-ui/react'
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
  provisionCluster,
  reloadCertificates,
  resetFailOverCounter,
  resetSLA,
  rollingOptimize,
  rollingRestart,
  rotateCertificates,
  rotateDBCredential,
  switchOverCluster,
  toggleTraffic,
  unProvisionCluster
} from '../../../redux/clusterSlice'
import NewServerModal from '../../../components/Modals/NewServerModal'

function ClusterDetail({ selectedCluster }) {
  const dispatch = useDispatch()
  const {
    common: { isDesktop },
    cluster: {
      clusterMaster,
      loadingStates: { menuActions: menuActionsLoading }
    }
  } = useSelector((state) => state)

  const [isConfirmModalOpen, setIsConfirmModalOpen] = useState(false)
  const [isNewServerModalOpen, setIsNewServerModalOpen] = useState(false)
  const [confirmHandler, setConfirmHandler] = useState(null)
  const [confirmTitle, setConfirmTitle] = useState('')
  const confirmBootrapMessage =
    'Bootstrap operation will destroy your existing replication setup. \n Are you really sure?'

  const openConfirmModal = () => {
    setIsConfirmModalOpen(true)
  }
  const closeConfirmModal = () => {
    setIsConfirmModalOpen(false)
    setConfirmHandler(null)
    setConfirmTitle('')
  }

  const menuOptions = [
    {
      name: 'HA',
      subMenu: [
        {
          name: 'Reset Failover Counter',
          onClick: () => {
            openConfirmModal()
            setConfirmTitle('Reset failover counter?')
            setConfirmHandler(() => () => dispatch(resetFailOverCounter({ clusterName: selectedCluster?.name })))
          }
        },
        {
          name: 'Rotate SLA',
          onClick: () => {
            openConfirmModal()
            setConfirmTitle('Reset SLA?')
            setConfirmHandler(() => () => dispatch(resetSLA({ clusterName: selectedCluster?.name })))
          }
        },
        {
          name: 'Toggle Traffic',
          onClick: () => {
            openConfirmModal()
            setConfirmTitle('Toggle traffic?')
            setConfirmHandler(() => () => dispatch(toggleTraffic({ clusterName: selectedCluster?.name })))
          }
        },
        ...(clusterMaster?.state === 'Failed'
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
          : [
              {
                name: 'Switchover',
                onClick: () => {
                  openConfirmModal()
                  setConfirmTitle('Confirm switchover?')
                  setConfirmHandler(
                    () => () => dispatch(switchOverCluster({ clustclusterName: selectedCluster?.nameerName }))
                  )
                }
              }
            ])
      ]
    },
    {
      name: 'Provision',
      subMenu: [
        {
          name: 'New Monitor',
          onClick: () => {
            setIsNewServerModalOpen(true)
          }
        },
        {
          name: 'Provision Cluster',
          onClick: () => {
            openConfirmModal()
            setConfirmTitle('Provision cluster?')
            setConfirmHandler(() => () => dispatch(provisionCluster({ clusterName: selectedCluster?.name })))
          }
        },
        {
          name: 'Unprovision Cluster',
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
        { name: 'Set Database Credentials' },
        { name: 'Set Replication Credentials' },
        { name: 'Set ProxySQL Credentials' },
        { name: 'Set Maxscale Credentials' },
        { name: 'Set Sharding Proxy Credentials' },
        {
          name: 'Rotate Database Credentials',
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
          name: 'Rolling Optimize',
          onClick: () => {
            openConfirmModal()
            setConfirmTitle('Rolling optimize?')
            setConfirmHandler(() => () => dispatch(rollingOptimize({ clusterName: selectedCluster?.name })))
          }
        },
        {
          name: 'Rolling Restart',
          onClick: () => {
            openConfirmModal()
            setConfirmTitle('Rolling restart?')
            setConfirmHandler(() => () => dispatch(rollingRestart({ clusterName: selectedCluster?.name })))
          }
        },
        {
          name: 'Rotate Certificates',
          onClick: () => {
            openConfirmModal()
            setConfirmTitle('Rotate certificates?')
            setConfirmHandler(() => () => dispatch(rotateCertificates({ clusterName: selectedCluster?.name })))
          }
        },
        {
          name: 'Reload Certificates',
          onClick: () => {
            openConfirmModal()
            setConfirmTitle('Reload certificates?')
            setConfirmHandler(() => () => dispatch(reloadCertificates({ clusterName: selectedCluster?.name })))
          }
        },
        {
          name: 'Cancel Rolling Restart',
          onClick: () => {
            openConfirmModal()
            setConfirmTitle('Cancel Rolling Restart?')
            setConfirmHandler(() => () => dispatch(cancelRollingRestart({ clusterName: selectedCluster?.name })))
          }
        },
        {
          name: 'Cancel Rolling Reprove',
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
          onClick: () => {
            openConfirmModal()
            setConfirmTitle(confirmBootrapMessage)
            setConfirmHandler(() => () => dispatch(bootstrapMasterSlave({ clusterName: selectedCluster?.name })))
          }
        },
        {
          name: 'Master Slave Positional',
          onClick: () => {
            openConfirmModal()
            setConfirmTitle(confirmBootrapMessage)
            setConfirmHandler(() => () => dispatch(bootstrapMasterSlaveNoGtid({ clusterName: selectedCluster?.name })))
          }
        },
        {
          name: 'Multi Master',
          onClick: () => {
            openConfirmModal()
            setConfirmTitle(confirmBootrapMessage)
            setConfirmHandler(() => () => dispatch(bootstrapMultiMaster({ clusterName: selectedCluster?.name })))
          }
        },
        {
          name: 'Multi Master Ring',
          onClick: () => {
            openConfirmModal()
            setConfirmTitle(confirmBootrapMessage)
            setConfirmHandler(() => () => dispatch(bootstrapMultiMasterRing({ clusterName: selectedCluster?.name })))
          }
        },
        {
          name: 'Multi Tier Slave',
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
          onClick: () => {
            openConfirmModal()
            setConfirmTitle('Confirm reload config?')
            setConfirmHandler(() => () => dispatch(configReload({ clusterName: selectedCluster?.name })))
          }
        },
        {
          name: 'Database discover config',
          onClick: () => {
            openConfirmModal()
            setConfirmTitle('Confirm database discover config?')
            setConfirmHandler(() => () => dispatch(configDiscoverDB({ clusterName: selectedCluster?.name })))
          }
        },
        {
          name: 'Database apply dynamic config',
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
      subMenu: [{ name: 'Clusters' }, { name: 'Servers' }, { name: 'Proxies' }]
    }
  ]

  const dataObject = [
    { key: 'Name', value: selectedCluster.name },
    { key: 'Orchestrator', value: selectedCluster.config.provOrchestrator },
    {
      key: 'Status',
      value: (
        <Wrap>
          {
            <>
              {selectedCluster.config.testInjectTraffic && <TagPill type='success' text='PrxTraffic' />}
              {selectedCluster.isProvision ? (
                <TagPill colorScheme='green' text='IsProvision' />
              ) : (
                <TagPill colorScheme='orange' text='NeedProvision' />
              )}
              {selectedCluster.isNeedDatabasesRollingRestart && (
                <TagPill colorScheme='orange' text='NeedRollingRestart' />
              )}
              {selectedCluster.isNeedDatabasesRollingReprov && (
                <TagPill colorScheme='orange' text='NeedRollingReprov' />
              )}
              {selectedCluster.isNeedDatabasesRestart && <TagPill colorScheme='orange' text='NeedDabaseRestart' />}
              {selectedCluster.isNeedDatabasesReprov && <TagPill colorScheme='orange' text='NeedDatabaseReprov' />}
              {selectedCluster.isNeedProxiesRestart && <TagPill colorScheme='orange' text='NeedProxyRestart' />}
              {selectedCluster.isNeedProxiesReprov && <TagPill colorScheme='orange' text='NeedProxyReprov' />}
              {selectedCluster.isNotMonitoring && <TagPill colorScheme='orange' text='UnMonitored' />}
              {selectedCluster.isCapturing && <TagPill colorScheme='orange' text='Capturing' />}
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
            <Box ml='auto'>
              {selectedCluster?.activePassiveStatus === 'A' ? (
                <TagPill colorScheme='green' text={'Active'} />
              ) : selectedCluster?.activePassiveStatus === 'S' ? (
                <TagPill colorScheme='orange' text={'Standby'} />
              ) : null}
            </Box>
          </>
        }
        body={<TableType2 dataArray={dataObject} />}
        headerAction='menu'
        isLoading={menuActionsLoading}
        menuOptions={menuOptions}
      />
      {isConfirmModalOpen && (
        <ConfirmModal
          isOpen={isConfirmModalOpen}
          closeModal={closeConfirmModal}
          title={confirmTitle}
          onConfirmClick={() => {
            confirmHandler()
            closeConfirmModal()
          }}
        />
      )}

      {isNewServerModalOpen && (
        <NewServerModal
          clusterName={selectedCluster?.name}
          isOpen={isNewServerModalOpen}
          closeModal={() => setIsNewServerModalOpen(false)}
        />
      )}
    </>
  )
}

export default ClusterDetail
