import React, { useEffect, useRef, useState } from 'react';
import { Terminal } from 'xterm';
import { FitAddon } from 'xterm-addon-fit';
import { AttachAddon } from 'xterm-addon-attach';
import 'xterm/css/xterm.css';
import { useParams } from 'react-router-dom';

const TerminalComponent = () => {
  const [status, setStatus] = useState('disconnected');
  const [url, setUrl] = useState('');
  const terminalRef = useRef(null);
  const terminalInstanceRef = useRef(null); // Store the terminal instance
  const socketRef = useRef(null); // Store the WebSocket connection in a ref for stability
  const { clustername, serverName, proxyName } = useParams();

  useEffect(() => {
    // Create a new terminal instance
    const terminal = new Terminal({
      cursorBlink: true,
      cols: 80,
      rows: 24,
    });

    // Attach the terminal to the DOM
    terminal.open(terminalRef.current);

    // Store the terminal instance
    terminalInstanceRef.current = terminal;

    // Initialize the fit addon
    const fitAddon = new FitAddon();
    terminal.loadAddon(fitAddon);

    // Resize the terminal to fit the container
    fitAddon.fit();

    terminal.writeln('Welcome to the Web Terminal!');

    // Automatically resize the terminal on window resize
    const resizeListener = () => fitAddon.fit();
    window.addEventListener("resize", resizeListener);

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
    if (clustername && serverName) {
      websocketUrl = `/api/terminal/connect/${clustername}/servers/${serverName}`;
    } else if (clustername && proxyName) {
      websocketUrl = `/api/terminal/connect/clusters/${clustername}/proxies/${proxyName}`;
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
      };

      socketRef.current.onclose = () => {
        setStatus('disconnected');
      };

    }
  }, [status, url]);

  return (
    <div>
      <h3>Web Terminal</h3>
      <div ref={terminalRef} style={{ height: '80vh', width: '100%', border: '1px solid #000' }}></div>

      <div style={{ marginTop: '10px' }}>
        {status === 'connected' ? (
          <button onClick={handleDisconnect}>Disconnect</button>
        ) : (
          <button onClick={handleConnect}>Connect</button>
        )}
      </div>

      <div>Status: {status}</div>
      <div>WebSocket URL: {url}</div>
      {status === 'connecting' && <p>Connecting...</p>}
      {status === 'disconnected' && <p>Disconnected. Try reconnecting.</p>}
      {status === 'error' && <p>Error occurred while connecting.</p>}
    </div>
  );
};

export default TerminalComponent;
