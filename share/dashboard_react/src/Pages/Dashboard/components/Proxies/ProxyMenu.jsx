import { useDispatch } from 'react-redux'
import MenuOptions from '../../../../components/MenuOptions'
import ConfirmModal from '../../../../components/Modals/ConfirmModal'
import { useState, useEffect, useCallback } from 'react'
import {
  abortProxy,
  clearProxy,
  dropServerByName,
  provisionProxy,
  stagingProxy,
  startProxy,
  stopProxy,
  unprovisionProxy
} from '../../../../redux/clusterSlice'
import { useHref } from 'react-router-dom'

function ProxyMenu({
  clusterName,
  row,
  isDesktop,
  colorScheme,
  from = 'tableView',
  user,
  isMenuOptionsVisible = false,
  showTerminal,
  topoStaging,
  orchestrator
}) {
  const dispatch = useDispatch()
  const [isConfirmModalOpen, setIsConfirmModalOpen] = useState(false)
  const [confirmTitle, setConfirmTitle] = useState('')
  const [confirmHandler, setConfirmHandler] = useState(null)
  const [proxyName, setProxyName] = useState('')
  const getHref = useHref('/').replace(/\/+$/, '');
      
  const openTerminalPage = useCallback((clusterName, prxId, commandType = '') => {
    const terminalURL = getHref.concat(`/terminal/clusters/${clusterName}/proxies/${prxId}/${commandType}`).replace(/\/+$/, '')
    window.open(terminalURL, '_blank')
  }, [getHref])

  useEffect(() => {
    if (row?.proxyId) {
      setProxyName(`${row.server} (${row.proxyId})`)
    }
  }, [row])

  const openConfirmModal = () => {
    setIsConfirmModalOpen(true)
  }
  const closeConfirmModal = () => {
    setIsConfirmModalOpen(false)
    setConfirmHandler(null)
    setConfirmTitle('')
  }

  return (
    <>
      <MenuOptions
        colorScheme={colorScheme}
        placement={from === 'tableView' ? 'right-end' : 'left-end'}
        subMenuPlacement={isDesktop ? (from === 'tableView' ? 'right-end' : 'left-end') : 'bottom'}
        options={[
          ...(topoStaging
            ? row.isStaging
              ? [
                  {
                    name: 'Set As Normal Proxy',
                    isDisabled: !user?.grants['cluster-staging'],
                    onClick: () => {
                      openConfirmModal()
                      setConfirmTitle(`Confirm provision proxy ${proxyName}?`)
                      setConfirmHandler(() => () => dispatch(stagingProxy({ clusterName, proxyId: row.proxyId, staging: false })))
                    }
                  }
                ]
              : [
                  {
                    name: 'Set As Staging Proxy',
                    isDisabled: !user?.grants['cluster-staging'],
                    onClick: () => {
                      openConfirmModal()
                      setConfirmTitle(`Confirm provision proxy ${proxyName}?`)
                      setConfirmHandler(() => () => dispatch(stagingProxy({ clusterName, proxyId: row.proxyId, staging: true })))
                    }
                  }
                ]
            : []),
          ...(isMenuOptionsVisible
            ? [
                {
                  name: 'Provision Proxy',
                  isDisabled: !user?.grants['prov-proxy-provision'],
                  onClick: () => {
                    openConfirmModal()
                    setConfirmTitle(`Confirm provision proxy ${proxyName}?`)
                    setConfirmHandler(() => () => dispatch(provisionProxy({ clusterName, proxyId: row.proxyId })))
                  }
                },
                {
                  name: 'Unprovision Proxy',
                  isDisabled: !user?.grants['prov-proxy-unprovision'],
                  onClick: () => {
                    openConfirmModal()
                    setConfirmTitle(`Confirm unprovision proxy ${proxyName}?`)
                    setConfirmHandler(() => () => dispatch(unprovisionProxy({ clusterName, proxyId: row.proxyId })))
                  }
                }
              ]
            : []),
          {
            name: 'Start Proxy',
            isDisabled: !user?.grants['proxy-start'],
            onClick: () => {
              openConfirmModal()
              setConfirmTitle(`Confirm start proxy ${proxyName}?`)
              setConfirmHandler(() => () => dispatch(startProxy({ clusterName, proxyId: row.proxyId })))
            }
          },
          {
            name: 'Stop Proxy',
            isDisabled: !user?.grants['proxy-stop'],
            onClick: () => {
              openConfirmModal()
              setConfirmTitle(`Confirm stop proxy ${proxyName}?`)
              setConfirmHandler(() => () => dispatch(stopProxy({ clusterName, proxyId: row.proxyId })))
            }
          },
          ...(orchestrator === 'opensvc'
            ? [
                {
                  name: 'Abort Orchestration',
                  isDisabled: !user?.grants['proxy-stop'],
                  onClick: () => {
                    openConfirmModal()
                    setConfirmTitle(`Confirm orchestration abort for ${proxyName}?`)
                    setConfirmHandler(() => () => dispatch(abortProxy({ clusterName, proxyId: row.proxyId })))
                  }
                },
                {
                  name: 'Clear Instance State',
                  isDisabled: !user?.grants['proxy-start'],
                  onClick: () => {
                    openConfirmModal()
                    setConfirmTitle(`Confirm clear instance state for ${proxyName}?`)
                    setConfirmHandler(() => () => dispatch(clearProxy({ clusterName, proxyId: row.proxyId })))
                  }
                }
              ]
            : []),
          ...(showTerminal
            ? [
                {
                  name: 'Web Terminal',
                  subMenu: [
                    {
                      name: 'MySQL Terminal',
                      isDisabled: !user?.grants['terminal-proxy'],
                      onClick: () => openTerminalPage(clusterName, row.proxyId, 'mysql')
                    },
                    {
                      name: 'MyTop Terminal',
                      isDisabled: !user?.grants['terminal-proxy'],
                      onClick: () => openTerminalPage(clusterName, row.proxyId, 'mytop')
                    },
                    {
                      name: 'Shell Terminal',
                      isDisabled: !user?.grants['terminal-global'],
                      onClick: () => openTerminalPage(clusterName, row.proxyId)
                    }
                  ]
                }
              ]
            : []),
          {
            name: 'Remove Monitor',
            isDisabled: !user?.grants['cluster-drop-monitor'],
            onClick: () => {
              openConfirmModal()
              setConfirmTitle(`Confirm removing monitor for ${row.proxyId} (${row.server})?`)
              setConfirmHandler(() => () => dispatch(dropServerByName({ clusterName, serverName: row.proxyId })))
            }
          }
        ]}
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
    </>
  )
}

export default ProxyMenu
