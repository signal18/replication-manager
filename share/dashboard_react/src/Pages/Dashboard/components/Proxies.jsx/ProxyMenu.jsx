import { useDispatch } from 'react-redux'
import MenuOptions from '../../../../components/MenuOptions'
import ConfirmModal from '../../../../components/Modals/ConfirmModal'
import { useState, useEffect } from 'react'
import { provisionProxy, startProxy, stopProxy, unprovisionProxy } from '../../../../redux/clusterSlice'

function ProxyMenu({ clusterName, row, isDesktop, colorScheme, from = 'tableView', user }) {
  const dispatch = useDispatch()
  const [isConfirmModalOpen, setIsConfirmModalOpen] = useState(false)
  const [confirmTitle, setConfirmTitle] = useState('')
  const [confirmHandler, setConfirmHandler] = useState(null)
  const [proxyName, setProxyName] = useState('')

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
  console.log('user::', user)

  return (
    <>
      <MenuOptions
        colorScheme={colorScheme}
        placement={from === 'tableView' ? 'right-end' : 'left-end'}
        subMenuPlacement={isDesktop ? (from === 'tableView' ? 'right-end' : 'left-end') : 'bottom'}
        options={[
          ...(user?.grants['prov-proxy-provision']
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
          ...(user?.grants['prov-proxy-unprovision']
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
          ...(user?.grants['proxy-start']
            ? [
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
          ...(user?.grants['proxy-stop']
            ? [
                {
                  name: 'Stop Proxy',
                  onClick: () => {
                    openConfirmModal()
                    setConfirmTitle(`Confirm stop proxy ${proxyName}?`)
                    setConfirmHandler(() => () => dispatch(stopProxy({ clusterName, proxyId: row.proxyId })))
                  }
                }
              ]
            : [])
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
