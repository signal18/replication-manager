import { FormControl, FormLabel, Select, Text } from '@chakra-ui/react'
import { useState } from 'react'
import { useDispatch } from 'react-redux'
import ConfirmModal from '../../../../components/Modals/ConfirmModal'
import { restartApp } from '../../../../redux/clusterSlice'

function buildContainerOptions(gitClones) {
  // namespace container is #01; git clone init containers start at #02
  return (gitClones || []).map((gc, i) => ({
    label: `Git container: ${gc.name}`,
    rid: `container#${String(i + 2).padStart(2, '0')}init${gc.name}`
  }))
}

function RestartAppModal({ isOpen, closeModal, clusterName, appId, appName, gitClones }) {
  const dispatch = useDispatch()
  const [selectedRid, setSelectedRid] = useState('')

  const containerOptions = buildContainerOptions(gitClones)

  const handleConfirm = () => {
    dispatch(restartApp({ clusterName, appId, rid: selectedRid }))
    setSelectedRid('')
    closeModal()
  }

  const handleClose = () => {
    setSelectedRid('')
    closeModal()
  }

  const selectedLabel = selectedRid
    ? containerOptions.find((o) => o.rid === selectedRid)?.label ?? selectedRid
    : 'full service'

  const body = (
    <FormControl>
      <FormLabel fontSize='sm'>Select what to restart</FormLabel>
      <Select value={selectedRid} onChange={(e) => setSelectedRid(e.target.value)} mb={3}>
        <option value=''>Full service restart</option>
        {containerOptions.map((opt) => (
          <option key={opt.rid} value={opt.rid}>
            {opt.label}
          </option>
        ))}
      </Select>
      <Text fontSize='xs' color='gray.500'>
        Restarting: <strong>{selectedLabel}</strong> for {appName}
      </Text>
    </FormControl>
  )

  return (
    <ConfirmModal
      isOpen={isOpen}
      closeModal={handleClose}
      title={`Restart app: ${appName}`}
      body={body}
      onConfirmClick={handleConfirm}
      confirmButtonText='Restart'
    />
  )
}

export default RestartAppModal
