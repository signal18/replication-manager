import { useDispatch } from 'react-redux'
import MenuOptions from '../../../../components/MenuOptions'
import ConfirmModal from '../../../../components/Modals/ConfirmModal'
import { useState, useEffect } from 'react'
import {
  abortApp,
  clearApp,
  dropApp,
  provisionApp,
  startApp,
  stopApp,
  unprovisionApp,
  updateRoutesApp
} from '../../../../redux/clusterSlice'
import { useNavigate } from 'react-router-dom'

function AppMenu({ clusterName, row, isDesktop, colorScheme, from = 'tableView', user, orchestrator }) {
  const dispatch = useDispatch()
  const [isConfirmModalOpen, setIsConfirmModalOpen] = useState(false)
  const [confirmTitle, setConfirmTitle] = useState('')
  const [confirmHandler, setConfirmHandler] = useState(null)
  const [appName, setAppName] = useState('')
  const navigate = useNavigate()

  useEffect(() => {
    if (row?.appId) {
      setAppName(`${row.server} (${row.appId})`)
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
          {
            name: 'Provision',
            subMenu: [
              ...(user?.grants['app-stop']
                ? [
                  {
                    name: 'Stop App',
                    onClick: () => {
                      openConfirmModal()
                      setConfirmTitle(`Confirm stop for ${appName}?`)
                      setConfirmHandler(() => () => dispatch(stopApp({ clusterName, appId: row.id })))
                    }
                  },
                  ...(orchestrator === 'opensvc'
                    ? [
                        {
                          name: 'Abort Orchestration',
                          onClick: () => {
                            openConfirmModal()
                            setConfirmTitle(`Confirm orchestration abort for ${appName}?`)
                            setConfirmHandler(() => () => dispatch(abortApp({ clusterName, appId: row.id })))
                          }
                        }
                      ]
                    : [])
                ]
                : []),
              ...(user?.grants['app-start']
                ? [
                  {
                    name: 'Start App',
                    onClick: () => {
                      openConfirmModal()
                      setConfirmTitle(`Confirm start for ${appName}?`)
                      setConfirmHandler(() => () => dispatch(startApp({ clusterName, appId: row.id })))
                    }
                  },
                  ...(orchestrator === 'opensvc'
                    ? [
                        {
                          name: 'Clear Instance State',
                          onClick: () => {
                            openConfirmModal()
                            setConfirmTitle(`Confirm clear instance state for ${appName}?`)
                            setConfirmHandler(() => () => dispatch(clearApp({ clusterName, appId: row.id })))
                          }
                        }
                      ]
                    : [])
                ]
                : []),
              ...(user?.grants['prov-app-provision']
                ? [
                  {
                    name: 'Provision App',
                    onClick: () => {
                      openConfirmModal()
                      setConfirmTitle(`Confirm provision ${appName}?`)
                      setConfirmHandler(() => () => dispatch(provisionApp({ clusterName, appId: row.id })))
                    }
                  }
                ]
                : []),
              ...(user?.grants['prov-app-provision'] && orchestrator === 'opensvc'
                ? [
                    {
                      name: 'Update Routes',
                      onClick: () => {
                        openConfirmModal()
                        setConfirmTitle(`Confirm update routes for ${appName}?`)
                        setConfirmHandler(() => () => dispatch(updateRoutesApp({ clusterName, appId: row.id })))
                      }
                    }
                  ]
                : []),
              ...(user?.grants['prov-app-unprovision']
                ? [
                  {
                    name: 'Unprovision App',
                    onClick: () => {
                      openConfirmModal()
                      setConfirmTitle(`Confirm unprovision for ${appName}?`)
                      setConfirmHandler(() => () => dispatch(unprovisionApp({ clusterName, appId: row.id })))
                    }
                  },
                ]
                : []),
              ...(user?.grants['prov-app-unprovision']
                ? [
                    {
                      name: 'Remove Monitor',
                      onClick: () => {
                        openConfirmModal()
                        setConfirmTitle(`Confirm removing monitor for ${appName}?`)
                        setConfirmHandler(() => () => dispatch(dropApp({ clusterName, host: row.host, port: row.port })))
                      }
                    }
                  ]
                : []),
            ]
          },
          ...(user?.grants['app-terminal'] ? [
            {
              name: 'Web Terminal',
              subMenu: [
                {
                  name: 'Shell Terminal',
                  onClick: () => navigate(`/terminal/clusters/${clusterName}/apps/${row.appId}`)
                }
              ]
            }
          ] : []),
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

export default AppMenu
