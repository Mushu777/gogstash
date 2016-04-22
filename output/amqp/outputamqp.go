package outputamqp

import (
	"errors"

	log "github.com/Sirupsen/logrus"
	"github.com/bitly/go-hostpool"
	"github.com/streadway/amqp"
	"github.com/tsaikd/gogstash/config"
	"github.com/tsaikd/gogstash/config/logevent"
)

// ModuleName is the name used in config file
const ModuleName = "amqp"

// OutputConfig holds the output configuration json fields and internal objects
type OutputConfig struct {
	config.OutputConfig
	URLs               []string `json:"urls"`                           // Array of AMQP connection strings formatted per the [RabbitMQ URI Spec](http://www.rabbitmq.com/uri-spec.html).
	RoutingKey         string   `json:"routing_key,omitempty"`          // The message routing key used to bind the queue to the exchange. Defaults to empty string.
	Exchange           string   `json:"exchange"`                       // AMQP exchange name
	ExchangeType       string   `json:"exchange_type"`                  // AMQP exchange type (fanout, direct, topic or headers).
	ExchangeDurable    bool     `json:"exchange_durable,omitempty"`     // Whether the exchange should be configured as a durable exchange. Defaults to false.
	ExchangeAutoDelete bool     `json:"exchange_auto_delete,omitempty"` // Whether the exchange is deleted when all queues have finished and there is no publishing. Defaults to true.
	Persistent         bool     `json:"persistent,omitempty"`           // Whether published messages should be marked as persistent or transient. Defaults to false.
	RetryCount         int      `json:"retry_count,omitempty"`          // Number of attempts for sending a message. Defaults to 3.
	hostPool           hostpool.HostPool
	amqpClients        map[string]amqpConn
	evchan             chan logevent.LogEvent
}

type amqpConn struct {
	Channel    *amqp.Channel
	Connection *amqp.Connection
}

// DefaultOutputConfig returns an OutputConfig struct with default values
func DefaultOutputConfig() OutputConfig {
	return OutputConfig{
		OutputConfig: config.OutputConfig{
			CommonConfig: config.CommonConfig{
				Type: ModuleName,
			},
		},
		RoutingKey:         "",
		ExchangeDurable:    false,
		ExchangeAutoDelete: true,
		Persistent:         false,
		RetryCount:         3,
		amqpClients:        map[string]amqpConn{},

		evchan: make(chan logevent.LogEvent),
	}
}

// InitHandler initialize the output plugin
func InitHandler(confraw *config.ConfigRaw) (retconf config.TypeOutputConfig, err error) {
	conf := DefaultOutputConfig()
	if err = config.ReflectConfig(confraw, &conf); err != nil {
		return
	}

	if err = conf.initAmqpClients(); err != nil {
		return
	}

	retconf = &conf
	return
}

func (o *OutputConfig) initAmqpClients() error {
	var hosts []string

	for _, url := range o.URLs {
		if conn, err := amqp.Dial(url); err == nil {
			if ch, err := conn.Channel(); err == nil {
				o.amqpClients[url] = amqpConn{Channel: ch, Connection: conn}
				err := ch.ExchangeDeclare(
					o.Exchange,
					o.ExchangeType,
					o.ExchangeDurable,
					o.ExchangeAutoDelete,
					false,
					false,
					nil,
				)
				if err != nil {
					return err
				}
				hosts = append(hosts, url)
			}
		}
	}

	if len(hosts) == 0 {
		return errors.New("no valid amqp server connection found")
	}

	o.hostPool = hostpool.New(hosts)
	return nil
}

// Event send the event through AMQP
func (o *OutputConfig) Event(event logevent.LogEvent) (err error) {
	raw, err := event.MarshalJSON()
	if err != nil {
		log.Errorf("event Marshal failed: %v", event)
		return
	}

	exchange := event.Format(o.Exchange)
	routingKey := event.Format(o.RoutingKey)

	for i := 0; i <= o.RetryCount; i++ {
		host := o.hostPool.Get().Host()
		err = o.amqpClients[host].Channel.Publish(
			exchange,
			routingKey,
			false,
			false,
			amqp.Publishing{
				ContentType: "application/json",
				Body:        raw,
			},
		)
		if err == nil {
			break
		}
	}

	return
}