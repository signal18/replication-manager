package metrics

import (
	"encoding/json"
	"github.com/Shopify/sarama"
	gologger "github.com/op/go-logging"
	"strconv"
	"time"
)

type KafkaProducer struct {
	metricsChannel chan Metric
	Log            *gologger.Logger
}

func (k *KafkaProducer) In(c chan Metric) {
	k.metricsChannel = c
}

func (k *KafkaProducer) Start(host string, port int) {

	connection := host + ":" + strconv.Itoa(port)

	k.Log.Notice("Connecting to Kafka on " + connection + "...")

	config := sarama.NewConfig()
	config.Metadata.Retry.Backoff = (10 * time.Second)

	/**
	 *  Set producer config
	 */
	// don't use zip compression
	config.Producer.Compression = 0

	// We are just streaming metrics, so don't not wait for any Kafka Acks.
	config.Producer.RequiredAcks = -1

	producer, err := sarama.NewSyncProducer([]string{connection}, config)
	if err != nil {

		k.Log.Error("Error connecting to Kafka: ", err.Error())
	} else {
		k.Log.Notice("Connection to Kafka successful")
	}

	go k.produce(producer)

}

func (k *KafkaProducer) produce(producer sarama.SyncProducer) {
	for {
		metric := <-k.metricsChannel
		json, err := json.MarshalIndent(metric, "", " ")
		if err != nil {
			return
		}
		msg := &sarama.ProducerMessage{Topic: "loadbalancer.all", Value: sarama.StringEncoder(json)}
		_, _, err = producer.SendMessage(msg)
		if err != nil {
			k.Log.Error("error sending to Kafka ")
		}
	}
}
