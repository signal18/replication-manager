import React, { useEffect, useRef, useState } from 'react';
import { Box, chakra, HStack, Select, Text } from '@chakra-ui/react';
import { Link, useNavigate, useParams, useSearchParams } from 'react-router-dom';
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
import {
  normalizeRIDSearchParams,
  OpenSVCTerminalRID,
  resolveServiceContainerFromRID,
  setRIDSearchParam,
} from './ridUtils';

const ChakraLink = chakra(Link);

const TerminalComponent = () => {
  const [status, setStatus] = useState('disconnected');
  const [url, setUrl] = useState('');
  const terminalRef = useRef(null);
  const terminalInstanceRef = useRef(null); // Store the terminal instance
  const socketRef = useRef(null); // Store the WebSocket connection in a ref for stability
  const { clusterName, serverName, proxyName, commandType } = useParams();
  const [searchParams, setSearchParams] = useSearchParams();
  const navigate = useNavigate();
  const [serviceContainer, setServiceContainer] = useState('container#db');

  const {
    cluster: { clusterData },
    auth: { baseURL },
  } = useSelector((state) => state);
  const dispatch = useDispatch();
  const normalizedCommandType = (commandType || 'bash').toLowerCase();
  const isOpenSVCServerShellTerminal = Boolean(clusterName && serverName) &&
    normalizedCommandType === 'bash' &&
    clusterData?.config?.provOrchestrator === 'opensvc';
  const ridParam = searchParams.get('rid');

  useEffect(() => {
    if (!isOpenSVCServerShellTerminal) {
      setServiceContainer(OpenSVCTerminalRID.Default);
      return;
    }

    setServiceContainer(resolveServiceContainerFromRID(ridParam));
  }, [isOpenSVCServerShellTerminal, ridParam]);

  useEffect(() => {
    const { params, changed } = normalizeRIDSearchParams(searchParams, isOpenSVCServerShellTerminal);
    if (changed) {
      setSearchParams(params, { replace: true });
    }
  }, [isOpenSVCServerShellTerminal, ridParam, searchParams, setSearchParams]);

  const handleServiceContainerChange = (event) => {
    const nextContainer = event.target.value;
    setServiceContainer(nextContainer);

    const nextParams = setRIDSearchParam(searchParams, nextContainer);
    setSearchParams(nextParams, { replace: true });
  };

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
  }, [clusterData?.name, serverName, proxyName])

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

    if (commandType && commandType !== "") {
      websocketUrl += `/${commandType}`;
    }

    if (isOpenSVCServerShellTerminal && serviceContainer === OpenSVCTerminalRID.Jobs) {
      websocketUrl += `?rid=${encodeURIComponent(serviceContainer)}`;
    }

    // Update state with the WebSocket URL
    setUrl(websocketUrl);
    setStatus('connecting');
  };

  const handleDisconnect = () => {
    if (socketRef.current) {
      // Intentionally defer status update to socket.onclose so UI reflects the
      // actual websocket lifecycle (including any brief close handshake).
      socketRef.current.close();
      return;
    }
    setStatus('disconnected');
  };

  useEffect(() => {
    if (status === 'connecting' && url) {
      if (socketRef.current && (socketRef.current.readyState === WebSocket.OPEN || socketRef.current.readyState === WebSocket.CONNECTING)) {
        socketRef.current.close();
      }

      // Once the WebSocket is connected, create the WebSocket instance
      const socket = new WebSocket(url);
      socketRef.current = socket;

      // Attach the terminal to the WebSocket using AttachAddon
      const attachAddon = new AttachAddon(socket);
      terminalInstanceRef.current.loadAddon(attachAddon);

      socket.onerror = () => {
        if (socketRef.current !== socket) {
          return;
        }
        console.error('WebSocket error');
        setStatus('error');
      };

      socket.onopen = () => {
        if (socketRef.current !== socket) {
          return;
        }
        setStatus('connected');
        socket.send(JSON.stringify({ type: 'auth', token: getTokenByBaseURL(baseURL) }));
      };

      socket.onclose = () => {
        if (socketRef.current !== socket) {
          return;
        }
        socketRef.current = null;
        setStatus('disconnected');
      };

    }
  }, [status, url, baseURL]);

  return (
    <PageContainer>
      <Box className={styles.container}>
        <HStack justify="space-between" align="center" spacing={4}>
          <Text fontSize="xl" fontWeight="bold" className={styles.title}>
            {clusterName ? (serverName ? "Server Terminal" : "Proxy Terminal") : "Web Terminal"}
          </Text>

          <HStack spacing={2}>
            {clusterName && (
              <>
                <ChakraLink to={`/clusters/${clusterName}`} fontSize="lg" fontWeight="bold" textDecoration="underline">
                  {clusterName}
                </ChakraLink>
                <Text fontSize="lg">{" >> "}</Text>
              </>
            )}
            <Text fontSize="lg" fontWeight="bold">
              {serverName || proxyName || ""}
            </Text>
          </HStack>
        </HStack>
        <div ref={terminalRef} style={{ height: '75vh', width: '100%', border: '1px solid #000' }}></div>
        {isOpenSVCServerShellTerminal && (
          <HStack mt={3} spacing={3}>
            <Text fontSize="sm">OpenSVC container</Text>
            <Select
              size="sm"
              maxW="280px"
              value={serviceContainer}
              onChange={handleServiceContainerChange}
              isDisabled={status === 'connected' || status === 'connecting'}
            >
              <option value={OpenSVCTerminalRID.Default}>container#db (default)</option>
              <option value={OpenSVCTerminalRID.Jobs}>container#jobs</option>
            </Select>
          </HStack>
        )}
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
