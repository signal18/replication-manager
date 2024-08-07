// ToastManager.js
import { useEffect } from 'react'
import { useSelector, useDispatch } from 'react-redux'
import { useToast } from '@chakra-ui/react'
import { resetToast } from '../redux/toastSlice'

const ToastManager = () => {
  const toast = useToast()
  const dispatch = useDispatch()
  const { status, title, description } = useSelector((state) => state.toast)

  useEffect(() => {
    if (status) {
      toast({
        title,
        description,
        status: status,
        duration: status === 'error' ? 5000 : 3000,
        isClosable: true,
        position: 'top-right'
      })
      dispatch(resetToast()) // Reset toast state after showing
    }
  }, [status, title, description, toast, dispatch])

  return null
}

export default ToastManager
