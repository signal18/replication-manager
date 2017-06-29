package zookeeper

import (
	"encoding/json"
	"github.com/magneticio/vamp-router/haproxy"
	gologger "github.com/op/go-logging"
	"github.com/samuel/go-zookeeper/zk"
	"strings"
	"time"
)

// simple struct for holding all Zookeeper related settings
type ZkClient struct {
	conn     *zk.Conn
	haConfig *haproxy.Config
	log      *gologger.Logger
}

func (z *ZkClient) Init(conString string, conf *haproxy.Config, log *gologger.Logger) error {

	z.log = log
	z.haConfig = conf
	err := z.connect(conString)

	if err != nil {
		return err
	}

	return nil
}

// connects to a zookeeper ensemble
func (z *ZkClient) connect(conString string) error {
	zks := strings.Split(conString, ",")
	conn, _, err := zk.Connect(zks, (60 * time.Second))

	if err != nil {
		return err
	}

	z.conn = conn
	return nil
}

/**
 * Watches a Zookeeper node continuously in a loop. When a watch fires, the new config is rendered.
 * When first registering the watch, the initial payload is also rendered
 */
func (z *ZkClient) Watch(path string) {

	go z.watcher(path)

}

func (z *ZkClient) watcher(path string) error {

	for {
		payload, _, watch, err := z.conn.GetW(path)

		if err != nil {
			z.log.Error("Error from Zookeeper: " + err.Error())
		}

		err = json.Unmarshal(payload, &z.haConfig)
		if err != nil {
			z.log.Error("Error parsing config from Zookeeper: " + err.Error())
		}

		// block till event fires
		event := <-watch

		z.log.Notice("Received Zookeeper event: " + event.Type.String())

		err = json.Unmarshal(payload, &z.haConfig)
		if err != nil {
			z.log.Error("Error parsing config from Zookeeper: " + err.Error())
		}

	}

}
