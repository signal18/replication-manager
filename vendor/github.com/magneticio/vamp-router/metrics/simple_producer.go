package metrics

import (
  "fmt"
  "encoding/json"
)

//  a very simple producer. It consumes the metrics stream and produces output on stdout in JSON format.
type SimpleProducer struct {
    metricsChannel chan Metric
}

func (s * SimpleProducer) In(c chan Metric){
  s.metricsChannel = c
}

func (s * SimpleProducer) Start(){
  go s.produce()
}

func (s *SimpleProducer) produce() {
  for {
    metric := <- s.metricsChannel
    json, err := json.MarshalIndent(metric, "", " ")
    if err != nil {
      return
    }
    fmt.Printf(string(json))
    }
}

