import { Suspense, useEffect } from 'react'
import { BrowserRouter, Navigate, Route, Routes, useLocation } from 'react-router-dom'
import { useDispatch, useSelector } from 'react-redux'
import PropTypes from 'prop-types'
import { Spinner, Center } from '@chakra-ui/react'
import './App.css'
import ToastManager from './components/ToastManager'
import SSOUpgradePoller from './components/SSOUpgradePoller'
import Login from './Pages/Login'
import Signup from './Pages/Signup'
// const Login = lazy(() => import('./Pages/Login'))
// const Home = lazy(() => import('./Pages/Home'))
import Home from './Pages/Home'
import Slideshow from './Pages/Slideshow'
import ClusterDB from './Pages/ClusterDB'
import TerminalComponent from './Pages/Terminal'
import ClusterApp from './Pages/ClusterApp'
import { whoami, logout } from './redux/authSlice'

const UNAVAILABLE_RETRY_MS = 15_000

// Dispatches the initial whoami on mount (if a token exists) and listens for
// server-side 401 events emitted by apiHelper so we can update Redux state
// without a full page reload. Also retries whoami when the server is unavailable.
function SessionGuard() {
  const dispatch = useDispatch()
  const sessionStatus = useSelector((state) => state.auth.sessionStatus)
  const unavailableRetryCount = useSelector((state) => state.auth.unavailableRetryCount)
  const { pathname } = useLocation()

  useEffect(() => {
    // /dashboard runs its own viewer-token polling loop (Login dashboard=true).
    // Running whoami there can race with a freshly fetched dashboard token and
    // clear it if the previous token was stale, breaking the slideshow recovery path.
    if (pathname === '/dashboard') return
    if (localStorage.getItem('user_token') && sessionStatus === 'unknown') {
      dispatch(whoami())
    }
  }, []) // eslint-disable-line react-hooks/exhaustive-deps

  useEffect(() => {
    const handle401 = () => dispatch(logout())
    window.addEventListener('repman:auth401', handle401)
    return () => window.removeEventListener('repman:auth401', handle401)
  }, [dispatch])

  // When the server was unreachable, retry whoami after a delay so the session
  // can recover once the backend comes back. Each failed attempt increments
  // unavailableRetryCount, which re-triggers this effect to schedule the next try.
  useEffect(() => {
    if (sessionStatus !== 'unavailable') return
    if (!localStorage.getItem('user_token')) return
    const timer = setTimeout(() => dispatch(whoami()), UNAVAILABLE_RETRY_MS)
    return () => clearTimeout(timer)
  }, [sessionStatus, unavailableRetryCount, dispatch])

  return null
}

function App() {
  return (
    <BrowserRouter>
      <SessionGuard />
      <ToastManager />
      <SSOUpgradePoller />
      <Routes>
        <Route
          path={'/'}
          element={
            <PrivateRoute>
              <Home />
            </PrivateRoute>
          }
        />
        <Route
          path={'/clusters/:cluster'}
          element={
            <PrivateRoute>
              <Home />
            </PrivateRoute>
          }
        />
        <Route
          path={'/clusters/:cluster/:dbname'}
          element={
            <PrivateRoute>
              <ClusterDB />
            </PrivateRoute>
          }
        />
        <Route
          path={'/clusters/:cluster/apps/:appname'}
          element={
            <PrivateRoute>
              <ClusterApp />
            </PrivateRoute>
          }
        />
        <Route path={"/terminal"} element={
          <PrivateRoute>
            <TerminalComponent />
          </PrivateRoute>
        } />
        <Route path={"/terminal/clusters/:clusterName/servers/:serverName"} element={
          <PrivateRoute>
            <TerminalComponent />
          </PrivateRoute>
        } />
        <Route path={"/terminal/clusters/:clusterName/servers/:serverName/:commandType"} element={
          <PrivateRoute>
            <TerminalComponent />
          </PrivateRoute>
        } />
        <Route path={"/terminal/clusters/:clusterName/proxies/:proxyName"} element={
          <PrivateRoute>
            <TerminalComponent />
          </PrivateRoute>
        } />
        <Route path={"/terminal/clusters/:clusterName/proxies/:proxyName/:commandType"} element={
          <PrivateRoute>
            <TerminalComponent />
          </PrivateRoute>
        } />
        <Route path={"/terminal/clusters/:clusterName/apps/:appName"} element={
          <PrivateRoute>
            <TerminalComponent />
          </PrivateRoute>
        } />
        <Route path={"/terminal/clusters/:clusterName/apps/:appName/:commandType"} element={
          <PrivateRoute>
            <TerminalComponent />
          </PrivateRoute>
        } />
        {/* /billing is served by Home — the Billing tab is rendered inside Home
            for SSO users when Cloud18 is enabled. This route allows deep-linking
            and programmatic navigation to that section without a dedicated page. */}
        <Route
          path='/billing'
          element={
            <PrivateRoute>
              <Home />
            </PrivateRoute>
          }
        />
        <Route path='/login' element={<Login />} />
        <Route path='/signup' element={<Signup />} />
        <Route path='/dashboard' element={<Login dashboard />} />
        <Route
          path='/slideshow'
          element={
            <SlideshowRoute>
              <Slideshow />
            </SlideshowRoute>
          }
        />
      </Routes>
    </BrowserRouter>
  )
}

const PrivateRoute = ({ children }) => {
  const sessionStatus = useSelector((state) => state.auth.sessionStatus)
  const hasToken = localStorage.getItem('user_token') !== null

  // No token at all — go to login immediately, no point waiting for whoami.
  if (!hasToken && sessionStatus === 'unknown') return <Navigate to='/login' />

  // Token exists but whoami hasn't resolved yet — hold here while SessionGuard validates.
  if (sessionStatus === 'unknown') {
    return <Center h='100vh'><Spinner size='xl' /></Center>
  }

  if (sessionStatus === 'unauthenticated') return <Navigate to='/login' />

  // 'authenticated' or 'unavailable': keep user in app while server restarts,
  // but if the token disappeared out-of-band, redirect immediately rather than
  // waiting for the next API call to discover it.
  if (!hasToken) return <Navigate to='/login' />

  return <Suspense fallback={<div>Loading...</div>}>{children}</Suspense>
}

// Slideshow uses the dashboard token — on auth failure redirect back to /dashboard
// (not /login) so the viewer token is re-fetched automatically.
const SlideshowRoute = ({ children }) => {
  const sessionStatus = useSelector((state) => state.auth.sessionStatus)
  const hasToken = localStorage.getItem('user_token') !== null

  if (!hasToken && sessionStatus === 'unknown') return <Navigate to='/dashboard' />
  if (sessionStatus === 'unknown') return <Center h='100vh'><Spinner size='xl' /></Center>
  if (sessionStatus === 'unauthenticated') return <Navigate to='/dashboard' />
  if (!hasToken) return <Navigate to='/dashboard' />

  return <Suspense fallback={<div>Loading...</div>}>{children}</Suspense>
}

PrivateRoute.propTypes = {
  children: PropTypes.node.isRequired
}

SlideshowRoute.propTypes = {
  children: PropTypes.node.isRequired
}

export default App
