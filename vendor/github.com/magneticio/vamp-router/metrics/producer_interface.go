package metrics


// 
type MetricsProducer interface {

  Consume(c chan Metric)
  Init()
  Produce()

}