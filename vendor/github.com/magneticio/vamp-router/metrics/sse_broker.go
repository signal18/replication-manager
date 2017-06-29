package metrics

import (
	"encoding/json"
	"fmt"
	gologger "github.com/op/go-logging"
	"net/http"
)

// most of this SSE implementation was taken from:
// https://github.com/kljensen/golang-html5-sse-example/blob/master/server.go

// The SSEBroker is responsible for keeping a list of which Clients (browsers)
// are currently attached and broadcasting events (messages) to those Clients.
type SSEBroker struct {

	// Create a map of Clients, the keys of the map are the channels
	// over which we can push messages to attached Clients.  (The values
	// are just booleans and are meaningless.)
	//
	Clients map[chan Metric]bool

	// Channel into which new Clients can be pushed
	//
	NewClients chan chan Metric

	// Channel into which disconnected Clients should be pushed
	//
	DefunctClients chan chan Metric

	// Channel into which messages are pushed to be broadcast out
	// to attahed Clients.
	//
	MetricsChannel chan Metric

	// the central logger
	Log *gologger.Logger
}

// This SSEBroker method starts a new goroutine.  It handles
// the addition & removal of Clients, as well as the broadcasting
// of messages out to Clients that are currently attached.
//
func (b *SSEBroker) Start() {

	counter := 0

	for {

		// Block until we receive from one of the
		// three following channels.
		select {

		case s := <-b.NewClients:

			// There is a new client attached and we
			// want to start sending them messages.
			b.Clients[s] = true
			b.Log.Notice("Added new SSE stream client")

		case s := <-b.DefunctClients:

			// A client has dettached and we want to
			// stop sending them messages.
			delete(b.Clients, s)
			b.Log.Notice("Removed SSE stream client")

		case metric := <-b.MetricsChannel:
			counter += 1
			// b.Log.Notice("received metrics in SSEBroker: %v", counter)
			for s, _ := range b.Clients {
				s <- metric
			}
		}
	}
}

// This SSEBroker method handles and HTTP request at the "/events/" URL.
//
func (b *SSEBroker) ServeHTTP(w http.ResponseWriter, r *http.Request) {

	// Make sure that the writer supports flushing.
	//
	f, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming unsupported!", http.StatusInternalServerError)
		return
	}

	// Create a new channel, over which the SSEBroker can
	// send this client messages.
	messageChan := make(chan Metric)

	// Add this client to the map of those that should
	// receive updates
	b.NewClients <- messageChan

	// Listen to the closing of the http connection via the CloseNotifier
	notify := w.(http.CloseNotifier).CloseNotify()
	go func() {
		<-notify
		// Remove this client from the map of attached Clients
		// when `EventHandler` exits.
		b.DefunctClients <- messageChan
		b.Log.Warning("HTTP connection for SSE stream just closed.")
	}()

	for {

		msg, open := <-messageChan

		if !open {
			break
		}

		json, err := json.Marshal(msg)
		if err != nil {
			return
		}
		fmt.Fprintf(w, "event: router-metric\ndata: %s\n\n", json)
		f.Flush()
	}
	// Done.
	b.Log.Notice("Finished HTTP stream request at ", r.URL.Path)
}
