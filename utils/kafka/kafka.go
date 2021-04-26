package kafka

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/confluentinc/confluent-kafka-go/kafka"
	uuid "github.com/satori/go.uuid"
	"github.com/siddontang/go-mysql/canal"
)

const showLogCnt = 20

type Streamer struct {
	brokers string
	//writers map[string]*kafkago.Writer
	producers     map[string]*kafka.Producer
	logsChan      chan kafka.LogEvent
	collectedLogs [showLogCnt]string
	logIndex      int
	logMutex      sync.Mutex
}

func NewStreamer(brokers string) Streamer {
	return Streamer{
		brokers: brokers,
		//	writers: make(map[string]*kafkago.Writer),
		producers: make(map[string]*kafka.Producer),
		logsChan:  make(chan kafka.LogEvent, 100000),
	}

}

// todo lower case keys from event
func (streamer *Streamer) WriteRow(topic string, partition int32, event *canal.RowsEvent) error {
	value, err := json.Marshal(event)

	if err != nil {
		fmt.Fprintf(os.Stderr, "CDC Error %s\n", err)
		return err
	}
	fmt.Fprintf(os.Stderr, "CDC Info %s", value)

	//	key := fmt.Sprintf("%s-%d-%d", event.Table, event.Header.LogPos, event.Header.Timestamp)
	u := uuid.NewV4()
	key := fmt.Sprintf("%s", u)
	producer := streamer.producers[topic+"_"+strconv.FormatInt(int64(partition), 10)]

	err = producer.Produce(
		&kafka.Message{
			TopicPartition: kafka.TopicPartition{
				Topic:     &topic,
				Partition: kafka.PartitionAny,
			},
			Key:   []byte(key),
			Value: value,
		}, nil)

	return err
	/*
		message := kafkago.Message{
			Topic: topic,
			Key:   []byte(key),
			Value: value,
			Time:  time.Time{},
		}
		return streamer.getWriter(topic).WriteMessages(context.Background(), message) */

}

func (streamer *Streamer) getURL() string {
	return fmt.Sprintf("%s", streamer.brokers)
}

func (streamer *Streamer) StartTransactionalProducer(topic string, partition int32) error {

	var toppar kafka.TopicPartition
	toppar.Topic = &topic
	toppar.Partition = partition

	producerConfig := &kafka.ConfigMap{
		"client.id":              fmt.Sprintf("txn-p%d", toppar.Partition),
		"bootstrap.servers":      streamer.brokers,
		"transactional.id":       fmt.Sprintf("replication-manager-cdc-p%d", int(toppar.Partition)),
		"go.logs.channel.enable": true,
		"go.logs.channel":        streamer.logsChan,
	}

	producer, err := kafka.NewProducer(producerConfig)
	if err != nil {
		return err
	}
	streamer.producers[topic+"_"+strconv.FormatInt(int64(partition), 10)] = producer
	streamer.addLog(fmt.Sprintf("Processor: created producer %s for partition %v",
		streamer.producers[topic+"_"+strconv.FormatInt(int64(partition), 10)], toppar.Partition))
	maxDuration, err := time.ParseDuration("10s")
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), maxDuration)
	defer cancel()

	err = producer.InitTransactions(ctx)
	if err != nil {
		return err
	}

	err = producer.BeginTransaction()
	if err != nil {
		return err
	}

	return nil
}

// destroyTransactionalProducer aborts the current transaction and destroys the producer.
func (streamer *Streamer) StopTransactionalProducer(topic string, partition int32) error {

	producer, found := streamer.producers[topic+"_"+strconv.FormatInt(int64(partition), 10)]
	if !found || producer == nil {
		return errors.New(fmt.Sprintf("BUG: No producer for input partition %s_%v", topic, partition))
	}
	maxDuration, err := time.ParseDuration("10s")
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), maxDuration)
	defer cancel()

	err = producer.AbortTransaction(ctx)
	if err != nil {
		if err.(kafka.Error).Code() == kafka.ErrState {
			// No transaction in progress, ignore the error.
			err = nil
		} else {
			streamer.addLog(fmt.Sprintf("Failed to abort transaction for %s: %s",
				producer, err))
		}
	}

	producer.Close()

	return err
}

// CommitTransactionalProducer sends the consumer offsets for
// the given input partition and commits the current transaction.
// A new transaction will be started when done.
func (streamer *Streamer) CommitTransactionalProducer(topic string, partition int32) error {
	producer, found := streamer.producers[topic+"_"+strconv.FormatInt(int64(partition), 10)]
	if !found {
		return errors.New(fmt.Sprintf("No producer for input partition %s_%v", topic, partition))
	}

	err := producer.CommitTransaction(nil)
	if err != nil {
		streamer.addLog(fmt.Sprintf(
			"Processor: Failed to commit transaction for input partition %v: %s",
			partition, err))

		err := producer.AbortTransaction(nil)
		if err != nil {
			return err
		}

		// Rewind this input partition to the last committed offset.
		//	rewindConsumerPosition(partition)
	}

	// Start a new transaction
	err = producer.BeginTransaction()
	if err != nil {
		return err
	}
	return nil
}

func (streamer *Streamer) addLog(log string) {

	streamer.logMutex.Lock()
	streamer.logIndex = (streamer.logIndex + 1) % len(streamer.collectedLogs)
	streamer.collectedLogs[streamer.logIndex] = log
	streamer.logMutex.Unlock()
	fmt.Fprintf(os.Stdout, "Add Kafka API log %s\n", log)

}
