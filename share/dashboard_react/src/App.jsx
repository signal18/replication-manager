import React, { lazy, Suspense } from 'react'
import { BrowserRouter, Navigate, Route, Routes } from 'react-router-dom'
import './App.css'
import ToastManager from './components/ToastManager'
import Login from './Pages/Login'
// const Login = lazy(() => import('./Pages/Login'))
// const Home = lazy(() => import('./Pages/Home'))
import Home from './Pages/Home'
import ClusterDB from './Pages/ClusterDB'
import TerminalComponent from './Pages/Terminal'

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
      </Routes>
    </BrowserRouter>
  )
}

export default App

const PrivateRoute = ({ children }) => {
  // Add your own authentication on the below line.
  const isLoggedIn = localStorage.getItem('user_token') !== null
  return isLoggedIn ? <Suspense fallback={<div>Loading...</div>}>{children}</Suspense> : <Navigate to='/login' />
}
