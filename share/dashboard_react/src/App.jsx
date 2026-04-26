import { Suspense } from 'react'
import { BrowserRouter, Navigate, Route, Routes } from 'react-router-dom'
import PropTypes from 'prop-types'
import './App.css'
import ToastManager from './components/ToastManager'
import Login from './Pages/Login'
// const Login = lazy(() => import('./Pages/Login'))
// const Home = lazy(() => import('./Pages/Home'))
import Home from './Pages/Home'
import Slideshow from './Pages/Slideshow'
import ClusterDB from './Pages/ClusterDB'
import TerminalComponent from './Pages/Terminal'
import ClusterApp from './Pages/ClusterApp'

function App() {
  return (
    <BrowserRouter>
      <ToastManager />
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
          path={'/clusters/:cluster/app/:appname'}
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
        <Route path='/login' element={<Login />} />
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
  const isLoggedIn = localStorage.getItem('user_token') !== null
  return isLoggedIn ? <Suspense fallback={<div>Loading...</div>}>{children}</Suspense> : <Navigate to='/login' />
}

// Slideshow uses the dashboard token — on auth failure redirect back to /dashboard
// (not /login) so the viewer token is re-fetched automatically.
const SlideshowRoute = ({ children }) => {
  const isLoggedIn = localStorage.getItem('user_token') !== null
  return isLoggedIn ? <Suspense fallback={<div>Loading...</div>}>{children}</Suspense> : <Navigate to='/dashboard' />
}

PrivateRoute.propTypes = {
  children: PropTypes.node.isRequired
}

SlideshowRoute.propTypes = {
  children: PropTypes.node.isRequired
}

export default App
