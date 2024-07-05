import React, { useEffect, lazy, Suspense } from 'react'
import { useDispatch, useSelector } from 'react-redux'
import { useNavigate } from 'react-router-dom'
import { logout, setUserData } from '../../redux/authSlice'
import { Box } from '@chakra-ui/react'
import { isAuthorized, getRefreshInterval } from '../../utility/common'
import { setRefreshInterval } from '../../redux/clusterSlice'
const Navbar = lazy(() => import('../Navbar'))

function PageContainer({ children }) {
  const dispatch = useDispatch()
  const navigate = useNavigate()
  const styles = {
    container: {
      display: 'flex',
      flexDirection: 'column',
      bg: '#eff2fe',
      height: '100%',
      width: '100%'
    }
  }

  const {
    auth: { isLogged, user },
    cluster: { refreshInterval }
  } = useSelector((state) => state)

  useEffect(() => {
    if (isAuthorized() && user === null) {
      dispatch(setUserData())
    }
    const interval = parseInt(getRefreshInterval())
    if (interval !== null && refreshInterval === 0) {
      dispatch(setRefreshInterval({ interval }))
    }
  }, [])

  useEffect(() => {
    if (!isLogged && user === null && !isAuthorized()) {
      navigate('/login')
    }
  }, [isLogged, user])

  const handleLogout = () => {
    dispatch(logout())
  }

  return (
    <Box sx={styles.container}>
      {isAuthorized() && (
        <Suspense fallback={<div>Loading...</div>}>
          <Navbar username={user?.username} />
        </Suspense>
      )}
      {children}
    </Box>
  )
}

export default PageContainer
