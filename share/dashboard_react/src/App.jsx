import React, { lazy, Suspense } from 'react'
import { BrowserRouter, Navigate, Route, Routes } from 'react-router-dom'
import './App.css'
import ToastManager from './components/ToastManager'
import Login from './Pages/Login'
// const Login = lazy(() => import('./Pages/Login'))
// const Home = lazy(() => import('./Pages/Home'))
import Home from './Pages/Home'
import ClusterDB from './Pages/ClusterDB'

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
