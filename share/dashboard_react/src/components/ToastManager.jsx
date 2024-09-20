// ToastManager.js
import { useEffect, useRef } from 'react'
import { useSelector, useDispatch } from 'react-redux'
import { useToast } from '@chakra-ui/react'
import { resetToast } from '../redux/toastSlice'

const ToastManager = () => {
  const toast = useToast()
  const dispatch = useDispatch()
  const toastIdRef = useRef()
  const { status, title, description } = useSelector((state) => state.toast)

  useEffect(() => {
    if (status === 'info') {
      // Show loader toast with indefinite duration
      toastIdRef.current = toast({
        title,
        description,
        status: 'info',
        duration: null, // Keeps the loader open
        isClosable: true,
        position: 'top-right'
      })
    } else if (status) {
      // If it's success or error, first close the loader toast (if any)
      if (toastIdRef.current) {
        toast.close(toastIdRef.current)
        toastIdRef.current = null
      }
      toast({
        title,
        description,
        status: status,
        duration: status === 'error' ? 5000 : status === 'success' ? 3000 : null,
        isClosable: true,
        position: 'top-right'
      })
      dispatch(resetToast()) // Reset toast state after showing
    }
  }, [status, title, description, toast, dispatch])

  return null
}

export default ToastManager
