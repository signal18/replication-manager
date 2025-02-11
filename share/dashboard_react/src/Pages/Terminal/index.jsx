import React, { useEffect, useRef, useState } from 'react';
import { Box, Text } from '@chakra-ui/react';
import { useNavigate, useParams } from 'react-router-dom';
import 'typeface-fira-code';
import PageContainer from '../PageContainer';
import { Terminal } from '@xterm/xterm';
import { FitAddon } from '@xterm/addon-fit';
import { AttachAddon } from '@xterm/addon-attach';
import '@xterm/xterm/css/xterm.css';
import styles from './styles.module.scss';
import { useDispatch, useSelector } from 'react-redux';
import { getClusterData } from '../../redux/clusterSlice';
import RMButton from '../../components/RMButton';
import { getTokenByBaseURL } from '../../services/apiHelper';

const TerminalComponent = () => {
  const [status, setStatus] = useState('disconnected');
  const [url, setUrl] = useState('');
  const terminalRef = useRef(null);
  const terminalInstanceRef = useRef(null); // Store the terminal instance
  const socketRef = useRef(null); // Store the WebSocket connection in a ref for stability
  const { clusterName, serverName, proxyName } = useParams();
  const navigate = useNavigate();
  
  const {
    cluster: { clusterData },
    auth: { baseURL },
  } = useSelector((state) => state);
  const dispatch = useDispatch();
  
  useEffect(() => {
    if (clusterName) {
      dispatch(getClusterData({ clusterName: clusterName }))
    }
  }, [clusterName])

  useEffect(() => {
    if (clusterData) {
      const servers = clusterData.dbServers || [];
      const proxies = clusterData.proxyServers || [];
  
      if (serverName) {
        if (!servers.find(srv => srv === serverName)) {
          navigate(`/`);
        }
      } else if (proxyName) {
        if (!proxies.find(prx => prx === proxyName)) {
          navigate(`/`);
        }
      }
    }
  }, [clusterData?.name,serverName,proxyName])

  const sendMessage = (message) => {
    if (socketRef.current && socketRef.current.readyState === WebSocket.OPEN) {
      socketRef.current.send(message);
    } else {
      console.error('WebSocket is not open.');
    }
  };


  useEffect(() => {
    // Create a new terminal instance with transparent background
    const terminal = new Terminal({
      allowTransparency: true,
      theme: {
        background: '#0000', // Transparent background
      },
      cursorBlink: true,
      cols: 120,
      rows: 25,
      fontFamily: 'Fira Code, monospace',
      fontWeight: 'normal',
    });

    terminal.open(terminalRef.current);

    // Store the terminal instance
    terminalInstanceRef.current = terminal;

    // Initialize the fit addon
    const fitAddon = new FitAddon();
    terminal.loadAddon(fitAddon);

    terminal.writeln('Welcome to the Web Terminal!');

    // Automatically resize the terminal on window resize
    const resizeListener = () => {
        fitAddon.fit(); // Resize terminal to fit container
        let dim = fitAddon.proposeDimensions(); // Propose dimensions to terminal
    
        // Send updated cols and rows to backend if connected
        if (socketRef.current && socketRef.current.readyState === WebSocket.OPEN) {
          sendMessage(`{"type":"resize","cols":${dim.cols},"rows":${dim.rows}}`);
        }
    }
    window.addEventListener("resize", resizeListener);

    resizeListener(); // Call resizeListener once to set initial size

    // Clean up on unmount
    return () => {
      if (socketRef.current) {
        socketRef.current.close();  // Close WebSocket connection
      }
      terminal.dispose();  // Dispose the terminal
      window.removeEventListener("resize", resizeListener);
    };

  }, []); // Empty dependency array ensures this effect runs only once

  const handleConnect = () => {
    let websocketUrl = '';
    if (clusterName && serverName) {
      websocketUrl = `/api/terminal/connect/clusters/${clusterName}/servers/${serverName}`;
    } else if (clusterName && proxyName) {
      websocketUrl = `/api/terminal/connect/clusters/${clusterName}/proxies/${proxyName}`;
    } else {
      websocketUrl = '/api/terminal/connect';
    }

    // Update state with the WebSocket URL
    setUrl(websocketUrl);
    setStatus('connecting');
  };

  const handleDisconnect = () => {
    if (socketRef.current) {
      socketRef.current.close();
    }
    setStatus('disconnected');
  };

  useEffect(() => {
    if (status === 'connecting' && url) {
      // Once the WebSocket is connected, create the WebSocket instance
      socketRef.current = new WebSocket(url);

      // Attach the terminal to the WebSocket using AttachAddon
      const attachAddon = new AttachAddon(socketRef.current);
      terminalInstanceRef.current.loadAddon(attachAddon);

      socketRef.current.onerror = () => {
        console.error('WebSocket error');
        setStatus('error');
      };

      socketRef.current.onopen = () => {
        setStatus('connected');
        socketRef.current.send(JSON.stringify({ type: 'auth', token: getTokenByBaseURL(baseURL) }));
      };

      socketRef.current.onclose = () => {
        setStatus('disconnected');
      };

    }
  }, [status, url]);

  return (
    <PageContainer>
      <Box className={styles.container}>
      { clusterName ? serverName ? (
        <Text fontSize="xl" fontWeight="bold" className={styles.title}>Server Terminal {clusterName} - {serverName}</Text>
      ) : (
        <Text fontSize="xl" fontWeight="bold" className={styles.title}>Proxy Terminal {clusterName} - {serverName}</Text>
      ) : (
        <Text fontSize="xl" fontWeight="bold" className={styles.title}>Web Terminal</Text>
      )
      }
      <div ref={terminalRef} style={{ height: '75vh', width: '100%', border: '1px solid #000' }}></div>
      <div style={{ marginTop: '10px' }}>
        {status === 'connected' ? (
          <RMButton onClick={handleDisconnect}>Disconnect</RMButton>
        ) : (
          <RMButton onClick={handleConnect}>Connect</RMButton>
        )}
      </div>

      <div>Status: {status}</div>
      <div>WebSocket URL: {url}</div>
      {status === 'connecting' && <p>Connecting...</p>}
      {status === 'disconnected' && <p>Disconnected. Try reconnecting.</p>}
      {status === 'error' && <p>Error occurred while connecting.</p>}
      </Box>
    </PageContainer>
  );
};

export default TerminalComponent;
