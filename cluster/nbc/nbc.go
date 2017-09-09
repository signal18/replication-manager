// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package nbc

import (
	"container/list"
	"sync"
)

// Special type that mimics the behavior of a channel but does not block when
// items are sent. Items are stored internally until received. Closing the Send
// channel will cause the Recv channel to be closed after all items have been
// received.
type NonBlockingChan struct {
	mutex sync.Mutex
	Send  chan<- interface{}
	Recv  <-chan interface{}
	items *list.List
}

// Loop for buffering items between the Send and Recv channels until the Send
// channel is closed.
func (n *NonBlockingChan) run(send <-chan interface{}, recv chan<- interface{}) {
	for {
		if send == nil && n.items.Len() == 0 {
			close(recv)
			break
		}
		var (
			recvChan chan<- interface{}
			recvVal  interface{}
		)
		if n.items.Len() > 0 {
			recvChan = recv
			recvVal = n.items.Front().Value
		}
		select {
		case i, ok := <-send:
			if ok {
				n.mutex.Lock()
				n.items.PushBack(i)
				n.mutex.Unlock()
			} else {
				send = nil
			}
		case recvChan <- recvVal:
			n.mutex.Lock()
			n.items.Remove(n.items.Front())
			n.mutex.Unlock()
		}
	}
}

// Create a new non-blocking channel.
func New() *NonBlockingChan {
	var (
		send = make(chan interface{})
		recv = make(chan interface{})
		n    = &NonBlockingChan{
			Send:  send,
			Recv:  recv,
			items: list.New(),
		}
	)
	go n.run(send, recv)
	return n
}

// Retrieve the number of items waiting to be received.
func (n *NonBlockingChan) Len() int {
	n.mutex.Lock()
	defer n.mutex.Unlock()
	return n.items.Len()
}
