import { useDispatch } from 'react-redux'
import MenuOptions from '../../../../components/MenuOptions'
import ConfirmModal from '../../../../components/Modals/ConfirmModal'
import RestartAppModal from './RestartAppModal'
import { useState } from 'react'
import {
  abortApp,
  clearApp,
  dropApp,
  provisionApp,
  startApp,
  stopApp,
  unprovisionApp,
  updateOpenSVCConfigApp,
  updateRoutesApp
} from '../../../../redux/clusterSlice'
import { useNavigate } from 'react-router-dom'

function AppMenu({ clusterName, row, isDesktop, colorScheme, from = 'tableView', user, orchestrator, collectorAPI }) {
  const dispatch = useDispatch()
  const canRestartApp = !!user?.grants['app-start'] && !!user?.grants['app-stop']
  const [isConfirmModalOpen, setIsConfirmModalOpen] = useState(false)
  const [isRestartModalOpen, setIsRestartModalOpen] = useState(false)
  const [confirmTitle, setConfirmTitle] = useState('')
  const [confirmHandler, setConfirmHandler] = useState(null)
  const appName = row?.id ? `${row.host}:${row.port} (${row.id})` : ''
  const navigate = useNavigate()

  const openConfirmModal = () => {
    setIsConfirmModalOpen(true)
  }
  const closeConfirmModal = () => {
    setIsConfirmModalOpen(false)
    setConfirmHandler(null)
    setConfirmTitle('')
  }

  const serviceMenuItems = [
    ...(user?.grants['app-start']
      ? [
          {
            name: 'Start App',
            onClick: () => {
              openConfirmModal()
              setConfirmTitle(`Confirm start for ${appName}?`)
              setConfirmHandler(() => () => dispatch(startApp({ clusterName, appId: row.id })))
            }
          }
        ]
      : []),
    ...(user?.grants['app-stop']
      ? [
          {
            name: 'Stop App',
            onClick: () => {
              openConfirmModal()
              setConfirmTitle(`Confirm stop for ${appName}?`)
              setConfirmHandler(() => () => dispatch(stopApp({ clusterName, appId: row.id })))
            }
          }
        ]
      : []),
    ...(canRestartApp && orchestrator === 'opensvc'
      ? [
          {
            name: 'Restart App',
            onClick: () => setIsRestartModalOpen(true)
          }
        ]
      : []),
    ...(user?.grants['app-stop'] && orchestrator === 'opensvc'
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
      : []),
    ...(user?.grants['app-start'] && orchestrator === 'opensvc'
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

  const provisionMenuItems = [
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
          },
          ...(collectorAPI === false
            ? [
                {
                  name: 'Push Config/Secret Maps',
                  onClick: () => {
                    openConfirmModal()
                    setConfirmTitle(`Confirm push config/secret maps for ${appName}?`)
                    setConfirmHandler(() => () => dispatch(updateOpenSVCConfigApp({ clusterName, appId: row.id })))
                  }
                }
              ]
            : [])
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
          {
            name: 'Remove Monitor',
            onClick: () => {
              openConfirmModal()
              setConfirmTitle(`Confirm removing monitor for ${appName}?`)
              setConfirmHandler(() => () => dispatch(dropApp({ clusterName, host: row.host, port: row.port })))
            }
          }
        ]
      : [])
  ]

  const topLevelOptions = [
    ...(serviceMenuItems.length > 0
      ? [{ name: 'Service', subMenu: serviceMenuItems }]
      : []),
    ...(provisionMenuItems.length > 0
      ? [{ name: 'Provision', subMenu: provisionMenuItems }]
      : []),
    ...(user?.grants['terminal-app']
      ? [
          {
            name: 'Web Terminal',
            subMenu: [
              {
                name: 'Shell Terminal',
                onClick: () => navigate(`/terminal/clusters/${clusterName}/apps/${row.id}`)
              }
            ]
          }
        ]
      : [])
  ]

  if (topLevelOptions.length === 0) {
    return null
  }

  return (
    <>
      <MenuOptions
        colorScheme={colorScheme}
        placement={from === 'tableView' ? 'right-end' : 'left-end'}
        subMenuPlacement={isDesktop ? (from === 'tableView' ? 'right-end' : 'left-end') : 'bottom'}
        options={topLevelOptions}
      />
      {isConfirmModalOpen && (
        <ConfirmModal
          isOpen={isConfirmModalOpen}
          closeModal={closeConfirmModal}
          title={confirmTitle}
          onConfirmClick={() => {
            confirmHandler?.()
            closeConfirmModal()
          }}
        />
      )}
      {isRestartModalOpen && (
        <RestartAppModal
          isOpen={isRestartModalOpen}
          closeModal={() => setIsRestartModalOpen(false)}
          clusterName={clusterName}
          appId={row.id}
          appName={appName}
          gitClones={row.config?.deployment?.storages?.gitClones}
        />
      )}
    </>
  )
}

export default AppMenu
