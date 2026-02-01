import { useEffect, useState } from 'react'
import { useDispatch } from 'react-redux'
import { getResticSnapshot } from '../../../redux/clusterSlice'

const isBackupOperation = (operationType) =>
  operationType === 'logical-backup' || operationType === 'physical-backup'

const useResticSnapshots = ({ isOpen, useResticSnapshot, clusterName, operationType, filter }) => {
  const dispatch = useDispatch()
  const [isLoadingSnapshots, setIsLoadingSnapshots] = useState(false)

  useEffect(() => {
    if (!isOpen) {
      return
    }
    if (!useResticSnapshot) {
      return
    }
    if (clusterName && isBackupOperation(operationType)) {
      setIsLoadingSnapshots(true)
      dispatch(getResticSnapshot({ clusterName, filter })).finally(() => setIsLoadingSnapshots(false))
    }
  }, [isOpen, useResticSnapshot, clusterName, operationType, filter, dispatch])

  return { isLoadingSnapshots }
}

export default useResticSnapshots
