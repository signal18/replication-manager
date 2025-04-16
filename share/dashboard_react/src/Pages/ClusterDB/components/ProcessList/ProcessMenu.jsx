import { useEffect, useState } from "react"
import { useDispatch } from "react-redux"
import MenuOptions from "../../../../components/MenuOptions"
import { killQuery, killThread } from "../../../../redux/clusterSlice"
import ConfirmModal from "../../../../components/Modals/ConfirmModal"

function ProcessMenu({ clusterName, serverId, row, isDesktop, colorScheme, from = 'tableView', user }) {
  const dispatch = useDispatch()
  const [isConfirmModalOpen, setIsConfirmModalOpen] = useState(false)
  const [confirmTitle, setConfirmTitle] = useState('')
  const [confirmHandler, setConfirmHandler] = useState(null)

  useEffect(() => {
  }, [row, serverId])

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
            ...(user?.grants['db-kill'] ? [{
              name: 'Kill Connection',
              onClick: () => {
                openConfirmModal()
                setConfirmTitle(`Confirm kill connection ${row.id}?`)
                setConfirmHandler(() => () => dispatch(killThread({ clusterName, serverId, queryDigest: row.id })))
              }
            },
            {
              name: 'Kill Query',
              onClick: () => {
                openConfirmModal()
                setConfirmTitle(`Confirm kill connection ${row.id}?`)
                setConfirmHandler(() => () => dispatch(killQuery({ clusterName, serverId, queryDigest: row.id })))
              }
            }] : [])
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

export default ProcessMenu
