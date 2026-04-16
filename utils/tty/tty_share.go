package tty

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sync"
	"sync/atomic"

	"github.com/gorilla/websocket"
	"github.com/siddontang/go/log"
)

const (
	MsgIDWrite   = "Write"
	MsgIDWinSize = "WinSize"
)

// Message used to encapsulate the rest of the bessages bellow
type MsgWrapper struct {
	Type string
	Data []byte
}

type MsgTTYWrite struct {
	Data []byte
	Size int
}

type MsgTTYWinSize struct {
	Cols int
	Rows int
}

type OnMsgWrite func(data []byte)
type OnMsgWinSize func(cols, rows int)

type TTYProtocolWSLocked struct {
	rsize  MsgTTYWinSize
	lsize  MsgTTYWinSize
	ioflag uint32
	ws     *websocket.Conn
	lock   sync.Mutex
}

func NewTTYProtocolWSLocked(ws *websocket.Conn) *TTYProtocolWSLocked {
	return &TTYProtocolWSLocked{
		ws: ws,
	}
}

func marshalMsg(aMessage interface{}) (_ []byte, err error) {
	var msg MsgWrapper

	if writeMsg, ok := aMessage.(MsgTTYWrite); ok {
		msg.Type = MsgIDWrite
		msg.Data, err = json.Marshal(writeMsg)
		//fmt.Printf("Sent write message %s\n", string(writeMsg.Data))
		if err != nil {
			return
		}
		return json.Marshal(msg)
	}

	if winChangedMsg, ok := aMessage.(MsgTTYWinSize); ok {
		msg.Type = MsgIDWinSize
		msg.Data, err = json.Marshal(winChangedMsg)
		if err != nil {
			return
		}
		return json.Marshal(msg)
	}

	return nil, nil
}

func (handler *TTYProtocolWSLocked) GetWebsocketConn() *websocket.Conn {
	return handler.ws
}

func (handler *TTYProtocolWSLocked) Read(p []byte) (n int, err error) {
	var msg MsgWrapper

	_, r, err := handler.ws.NextReader()
	if err != nil {
		// underlaying conn is closed. signal that through io.EOF
		return 0, io.EOF
	}

	dec := json.NewDecoder(r)
	err = dec.Decode(&msg)
	if err != nil {
		return 0, err
	}

	if msg.Type == MsgIDWrite {
		var msgWrite MsgTTYWrite
		err = json.Unmarshal(msg.Data, &msgWrite)
		if err != nil {
			return 0, err
		}
		//fmt.Printf("Received write message %s\n", string(msgWrite.Data))
		copy(p, msgWrite.Data)
		return msgWrite.Size, nil
	} else if msg.Type == MsgIDWinSize {
		var msgWinSize MsgTTYWinSize
		err = json.Unmarshal(msg.Data, &msgWinSize)
		if err != nil {
			return 0, err
		}
		handler.OnRemoteResize(msgWinSize.Cols, msgWinSize.Rows)
		return 0, nil
	}

	return 0, nil
}

func (handler *TTYProtocolWSLocked) SetWinSize(cols, rows int) (err error) {
	msgWinChanged := MsgTTYWinSize{
		Cols: cols,
		Rows: rows,
	}
	data, err := marshalMsg(msgWinChanged)
	if err != nil {
		return
	}

	handler.lock.Lock()
	handler.lsize.Cols = cols
	handler.lsize.Rows = rows
	err = handler.ws.WriteMessage(websocket.TextMessage, data)
	handler.lock.Unlock()
	return
}

// Function to send data from one the sender to the server and the other way around.
func (handler *TTYProtocolWSLocked) Write(buff []byte) (n int, err error) {
	msgWrite := MsgTTYWrite{
		Data: buff,
		Size: len(buff),
	}
	data, err := marshalMsg(msgWrite)
	if err != nil {
		return 0, err
	}

	handler.lock.Lock()
	n, err = len(buff), handler.ws.WriteMessage(websocket.TextMessage, data)
	handler.lock.Unlock()
	return
}

func (handler *TTYProtocolWSLocked) Close() (err error) {
	if handler.ws == nil {
		return nil
	}
	handler.lock.Lock()
	err = handler.ws.Close()
	handler.lock.Unlock()
	return
}

func (handler *TTYProtocolWSLocked) OnRemoteResize(cols, rows int) {
	handler.lock.Lock()
	handler.rsize.Cols = cols
	handler.rsize.Rows = rows
	handler.lock.Unlock()

	if handler.lsize.Cols < cols || handler.lsize.Rows < rows {
		log.Warnf("Warning: remote terminal size %dx%d is larger than local terminal size %dx%d", cols, rows, handler.lsize.Cols, handler.lsize.Rows)
		atomic.StoreUint32(&handler.ioflag, 0)
	} else {
		atomic.StoreUint32(&handler.ioflag, 1)
	}
}

type TunInitMsg struct {
	Address string
}

type WSConnReadWriteCloser struct {
	WsConn *websocket.Conn
	reader io.Reader
}

func (conn *WSConnReadWriteCloser) Read(p []byte) (n int, err error) {
	// Weird method here, as we need to do a few things:
	//   - re-use the WS reader between different calls of this function. If the existing reader
	//       has no more data, then get another reader (NextReader())
	//   - if we get a CloseAbnormalClosure, or CloseGoingAway error message from WS, we need to
	//       transform that into a io.EOF, otherwise yamux will complain. We use yamux on top of this
	//       reader interface, in order to multiplex multiple streams
	// More here:
	// https://github.com/hashicorp/yamux/blob/574fd304fd659b0dfdd79e221f4e34f6b7cd9ed2/session.go#L554
	// https://github.com/gorilla/websocket/blob/b65e62901fc1c0d968042419e74789f6af455eb9/examples/chat/client.go#L67
	// https://stackoverflow.com/questions/61108552/go-websocket-error-close-1006-abnormal-closure-unexpected-eof

	filterErr := func() {

		if err != nil && !websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
			// if we have an error != nil, and it's one of the two, then return EOF
			err = io.EOF
		}
	}

	defer filterErr()

	if conn.reader != nil {
		n, err = conn.reader.Read(p)

		if err == io.EOF {
			// if this reader has no more data, get the next reader
			_, conn.reader, err = conn.WsConn.NextReader()

			if err == nil {
				// and read in this same call as well
				return conn.reader.Read(p)
			}
		}
	} else {
		_, conn.reader, err = conn.WsConn.NextReader()
	}
	return
}

func (conn *WSConnReadWriteCloser) Write(p []byte) (n int, err error) {
	return len(p), conn.WsConn.WriteMessage(websocket.BinaryMessage, p)
}

func (conn *WSConnReadWriteCloser) Close() error {
	return conn.WsConn.Close()
}

func (sm *SessionManager) HandleTtyShare(s *Session) error {
	httpURL, err := url.Parse(s.ServiceTtyUrl)
	if err != nil {
		return err
	}

	client := &http.Client{}

	if httpURL.Scheme == "https" {
		client.Transport = &http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: true,
			},
		}
	}

	resp, err := client.Get(s.ServiceTtyUrl)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("tty-share endpoint %s returned HTTP %d", s.ServiceTtyUrl, resp.StatusCode)
	}

	// Build the WS URL from the host part of the given http URL and the wsPath
	wsScheme := "ws"
	if httpURL.Scheme == "https" {
		wsScheme = "wss"
	}

	// Get the path of the websocket route from the header
	ttyWsPath := resp.Header.Get("TTYSHARE-TTY-WSPATH")
	if ttyWsPath == "" {
		return fmt.Errorf("tty-share endpoint %s missing TTYSHARE-TTY-WSPATH header", s.ServiceTtyUrl)
	}
	ttyWsURL := wsScheme + "://" + httpURL.Host + ttyWsPath

	dialer := websocket.DefaultDialer
	if httpURL.Scheme == "https" {
		dialer.TLSClientConfig = &tls.Config{
			// Set InsecureSkipVerify to true to disable certificate validation
			InsecureSkipVerify: true,
		}
	}

	sconn, _, err := dialer.Dial(ttyWsURL, nil)
	if err != nil {
		return err
	}
	defer sconn.Close()

	rw := NewTTYProtocolWSLocked(sconn)
	s.SourceConn = rw
	s.Stdin = rw
	s.Stdout = rw

	// Add session to manager to keep track of it
	sm.AddSession(s)

	// Start WebSocket and keep-alive mechanisms
	s.WG.Add(3)
	go func() {
		defer s.WG.Done()
		s.HandleWebSocketInput()
	}()
	go func() {
		defer s.WG.Done()
		s.HandleOutput(false)
	}()
	go func() {
		defer s.WG.Done()
		s.keepAliveWebSocket()
	}()

	s.WG.Wait()

	// Clean up session after the process exits
	sm.RemoveSession(s.ID)
	s.Close()

	return nil
}
