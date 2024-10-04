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
import parentStyles from '../styles.module.scss'
import CopyTextModal from '../../../components/Modals/CopyTextModal'
import SetCredentialsModal from '../../../components/Modals/SetCredentialsModal'

function ClusterDetail({ selectedCluster }) {
  const dispatch = useDispatch()
  const {
    common: { isDesktop },
    cluster: {
      clusterMaster,
      clusterServers,
      clusterProxies,
      loadingStates: { menuActions: menuActionsLoading }
    }
  } = useSelector((state) => state)

  const [isConfirmModalOpen, setIsConfirmModalOpen] = useState(false)
  const [isNewServerModalOpen, setIsNewServerModalOpen] = useState(false)
  const [isCredentialModalOpen, setIsCredentialModalOpen] = useState(false)
  const [isClipboardModalOpen, setIsClipboardModalOpen] = useState(false)
  const [clipboardText, setClipboardText] = useState('')
  const [confirmHandler, setConfirmHandler] = useState(null)
  const [confirmTitle, setConfirmTitle] = useState('')
  const [credentialType, setCredentialType] = useState('')
  const confirmBootrapMessage = 'Bootstrap operation will destroy your existing replication setup. \n Are you sure?'

  const openConfirmModal = () => {
    setIsConfirmModalOpen(true)
  }
  const closeConfirmModal = () => {
    setIsConfirmModalOpen(false)
    setIsClipboardModalOpen(false)
    setConfirmHandler(null)
    setConfirmTitle('')
    setClipboardText('')
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
        {
          name: 'Set Database Credentials',
          onClick: () => {
            setIsCredentialModalOpen(true)
            setCredentialType('Database Server Credential')
          }
        },
        {
          name: 'Set Replication Credentials',
          onClick: () => {
            setIsCredentialModalOpen(true)
            setCredentialType('Replication Credential')
          }
        },
        {
          name: 'Set ProxySQL Credentials',
          onClick: () => {
            setIsCredentialModalOpen(true)
            setCredentialType('ProxySQL Credential')
          }
        },
        {
          name: 'Set Maxscale Credentials',
          onClick: () => {
            setIsCredentialModalOpen(true)
            setCredentialType('Maxscale Credential')
          }
        },
        {
          name: 'Set Sharding Proxy Credentials',
          onClick: () => {
            setIsCredentialModalOpen(true)
            setCredentialType('Sharding Proxy Credential')
          }
        },
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
      subMenu: [
        {
          name: 'Clusters',
          onClick: () => {
            setIsClipboardModalOpen(true)
            setClipboardText(JSON.stringify(selectedCluster))
            setConfirmTitle('Json of selected cluster')
          }
        },
        {
          name: 'Servers',
          onClick: () => {
            setIsClipboardModalOpen(true)
            setClipboardText(JSON.stringify(clusterServers))
            setConfirmTitle('Json of database servers')
          }
        },
        {
          name: 'Proxies',
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

      {isClipboardModalOpen && (
        <CopyTextModal
          isOpen={isClipboardModalOpen}
          text={clipboardText}
          closeModal={closeConfirmModal}
          title={confirmTitle}
          showPrettyJsonCheckbox={true}
        />
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
