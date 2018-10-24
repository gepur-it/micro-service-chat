package main

import (
	"fmt"
	"github.com/joho/godotenv"
	"github.com/sirupsen/logrus"
	"github.com/streadway/amqp"
	"net/http"
	"os"
)

type ErpToSocketMessage struct {
	AppealID string                 `json:"appealId"`
	Message  map[string]interface{} `json:"message"`
}

var AMQPConnection *amqp.Connection
var AMQPChannel *amqp.Channel
var logger = logrus.New()

func failOnError(err error, msg string) {
	if err != nil {
		logger.WithFields(logrus.Fields{
			"error": err,
		}).Fatal(msg)
		panic(fmt.Sprintf("%s: %s", msg, err))
	}
}

func init() {
	err := godotenv.Load()
	if err != nil {
		panic(fmt.Sprintf("%s: %s", "Error loading .env file", err))
	}

	logger.SetLevel(logrus.DebugLevel)
	logger.SetOutput(os.Stdout)
	logger.SetFormatter(&logrus.TextFormatter{})

	cs := fmt.Sprintf("amqp://%s:%s@%s:%s/%s",
		os.Getenv("RABBITMQ_ERP_LOGIN"),
		os.Getenv("RABBITMQ_ERP_PASS"),
		os.Getenv("RABBITMQ_ERP_HOST"),
		os.Getenv("RABBITMQ_ERP_PORT"),
		os.Getenv("RABBITMQ_ERP_VHOST"))

	connection, err := amqp.Dial(cs)
	failOnError(err, "Failed to connect to RabbitMQ")
	AMQPConnection = connection

	channel, err := AMQPConnection.Channel()
	failOnError(err, "Failed to open a channel")
	AMQPChannel = channel

	failOnError(err, "Failed to declare a queue")
}

func ws(hub *Hub, w http.ResponseWriter, r *http.Request) {
	logger.WithFields(logrus.Fields{
		"addr":   r.RemoteAddr,
		"method": r.Method,
	}).Info("Socket connect:")

	conn, err := upgrader.Upgrade(w, r, nil)
	failOnError(err, "Can`t upgrade web socket")

	client := &Client{hub: hub, conn: conn, send: make(chan []byte, 256)}
	client.hub.register <- client

	go client.writePump()
	go client.readPump()
	go client.query()
}

func main() {
	hub := hub()

	go hub.run()

	http.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		ws(hub, w, r)
	})

	logger.WithFields(logrus.Fields{
		"port": os.Getenv("PACT_LISTEN_PORT"),
	}).Fatal(http.ListenAndServe(fmt.Sprintf(":%s", os.Getenv("PACT_LISTEN_PORT")), nil))

	defer AMQPConnection.Close()
	defer AMQPChannel.Close()
}
