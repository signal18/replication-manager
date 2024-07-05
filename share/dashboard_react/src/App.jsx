import React, { lazy } from 'react'
import { BrowserRouter, Navigate, Route, Routes } from 'react-router-dom'
import './App.css'
import PageContainer from './components/PageContainer'
const Login = lazy(() => import('./components/Login'))
const Dashboard = lazy(() => import('./components/Dashboard'))

function App() {
  return (
    <BrowserRouter>
      <Routes>
        <Route
          path='/'
          element={
            <PrivateRoute>
              <Dashboard />
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
  return isLoggedIn ? <>{children}</> : <Navigate to='/login' />
}
