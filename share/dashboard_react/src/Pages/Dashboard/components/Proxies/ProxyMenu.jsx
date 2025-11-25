import { useDispatch } from 'react-redux'
import MenuOptions from '../../../../components/MenuOptions'
import ConfirmModal from '../../../../components/Modals/ConfirmModal'
import { useState, useEffect, useCallback } from 'react'
import { dropServerByName, provisionProxy, stagingProxy, startProxy, stopProxy, unprovisionProxy } from '../../../../redux/clusterSlice'
import { useHref } from 'react-router-dom'

function ProxyMenu({ clusterName, row, isDesktop, colorScheme, from = 'tableView', user, isMenuOptionsVisible = false, showTerminal, topoStaging }) {
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
          ...(user?.grants['cluster-staging'] && topoStaging ? row.isStaging ? [
            {
              name: 'Set As Normal Proxy',
              onClick: () => {
                openConfirmModal()
                setConfirmTitle(`Confirm provision proxy ${proxyName}?`)
                setConfirmHandler(() => () => dispatch(stagingProxy({ clusterName, proxyId: row.proxyId, staging: false })))
              }
            }
          ] : [
            {
              name: 'Set As Staging Proxy',
              onClick: () => {
                openConfirmModal()
                setConfirmTitle(`Confirm provision proxy ${proxyName}?`)
                setConfirmHandler(() => () => dispatch(stagingProxy({ clusterName, proxyId: row.proxyId, staging: true })))
              }
            }
          ] : []),
          ...(user?.grants['prov-proxy-provision'] && isMenuOptionsVisible
            ? [
                {
                  name: 'Provision Proxy',
                  onClick: () => {
                    openConfirmModal()
                    setConfirmTitle(`Confirm provision proxy ${proxyName}?`)
                    setConfirmHandler(() => () => dispatch(provisionProxy({ clusterName, proxyId: row.proxyId })))
                  }
                }
            ]
            : []),
          ...(user?.grants['prov-proxy-unprovision'] && isMenuOptionsVisible
            ? [
                {
                  name: 'Unprovision Proxy',
                  onClick: () => {
                    openConfirmModal()
                    setConfirmTitle(`Confirm unprovision proxy ${proxyName}?`)
                    setConfirmHandler(() => () => dispatch(unprovisionProxy({ clusterName, proxyId: row.proxyId })))
                  }
                }
              ]
            : []),
          ...(user?.grants['proxy-start'] ? [
                {
                  name: 'Start Proxy',
                  onClick: () => {
                    openConfirmModal()
                    setConfirmTitle(`Confirm start proxy ${proxyName}?`)
                    setConfirmHandler(() => () => dispatch(startProxy({ clusterName, proxyId: row.proxyId })))
                  }
                }
              ]
            : []),
          ...(user?.grants['proxy-stop'] ? [
                {
                  name: 'Stop Proxy',
                  onClick: () => {
                    openConfirmModal()
                    setConfirmTitle(`Confirm stop proxy ${proxyName}?`)
                    setConfirmHandler(() => () => dispatch(stopProxy({ clusterName, proxyId: row.proxyId })))
                  }
                }
              ]
            : []),
          ...(user?.grants['terminal-proxy'] && showTerminal 
            ? [
                {
                  name: 'Web Terminal',
                  subMenu: [
                    {
                      name: 'MySQL Terminal',
                      onClick: () => openTerminalPage(clusterName, row.proxyId, 'mysql')
                    },
                    {
                      name: 'MyTop Terminal',
                      onClick: () => openTerminalPage(clusterName, row.proxyId, 'mytop')
                    },
                    ...(user?.grants['terminal-global'] ? [
                      {
                        name: 'Shell Terminal',
                        onClick: () => openTerminalPage(clusterName, row.proxyId)
                      }
                    ] : []),
                  ]
                }
              ] 
            : []),
          {
            name: 'Remove Monitor',
            onClick: () => {
              openConfirmModal()
              setConfirmTitle(`Confirm removing monitor for ${row.proxyId} (${row.server})?`)
              setConfirmHandler(() => () => dispatch(dropServerByName({ clusterName, serverName: row.proxyId })))
            }
          },
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
