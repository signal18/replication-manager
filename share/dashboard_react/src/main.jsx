import React from 'react'
import ReactDOM from 'react-dom/client'
import App from './App.jsx'
import './index.css'
import './styles/theme.scss'
import { Provider } from 'react-redux'
import store from './redux/store.js'
import { ChakraProvider } from '@chakra-ui/react'
import ThemeProvider from './ThemeProvider.jsx'

ReactDOM.createRoot(document.getElementById('root')).render(
  <Provider store={store}>
    <ChakraProvider>
      <ThemeProvider>
        <App />
      </ThemeProvider>
    </ChakraProvider>
  </Provider>
)
