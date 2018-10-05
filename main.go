package main

import (
	"fmt"
	"github.com/joho/godotenv"
	"github.com/sirupsen/logrus"
	"github.com/streadway/amqp"
	"net/http"
	"os"
)

type Message struct {
	ID             string        `json:"id"`
	Message        string        `json:"message"`
	Attachments    []interface{} `json:"attachments"`
	Direction      int           `json:"direction"`
	DeliveryStatus int           `json:"deliveryStatus"`
	CreatedAt      string        `json:"createdAt"`
	ReceivedAt     string        `json:"receivedAt"`
}

type ErpToSocketMessage struct {
	AppealID string  `json:"appealId"`
	Message  Message `json:"message"`
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

	if os.Getenv("APP_ENV") == "prod" {
		file, err := os.OpenFile(os.Getenv("LOG_FILE"), os.O_CREATE|os.O_WRONLY, 0666)

		if err != nil {
			panic(fmt.Sprintf("%s: %s", "Failed log to file", err))
		}

		logger.SetLevel(logrus.ErrorLevel)
		logger.SetOutput(file)
		logger.SetFormatter(&logrus.JSONFormatter{})
	} else {
		logger.SetLevel(logrus.DebugLevel)
		logger.SetOutput(os.Stdout)
		logger.SetFormatter(&logrus.TextFormatter{})
	}

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

func home(w http.ResponseWriter, r *http.Request) {

	logger.WithFields(logrus.Fields{
		"addr":   r.RemoteAddr,
		"method": r.Method,
	}).Info("Http connect:")

	if r.URL.Path != "/" {
		http.Error(w, "Not found", http.StatusNotFound)
		logger.WithFields(logrus.Fields{
			"addr":   r.RemoteAddr,
			"method": r.Method,
		}).Warn("Not allowed path:")
		return
	}

	if r.Method != "GET" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		logger.WithFields(logrus.Fields{
			"addr":   r.RemoteAddr,
			"method": r.Method,
		}).Warn("Not allowed method:")
		return
	}

	http.ServeFile(w, r, "home.html")
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

	http.HandleFunc("/", home)
	http.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		ws(hub, w, r)
	})

	logger.WithFields(logrus.Fields{
		"port": os.Getenv("PACT_LISTEN_PORT"),
	}).Fatal(http.ListenAndServe(fmt.Sprintf(":%s", os.Getenv("PACT_LISTEN_PORT")), nil))

	defer AMQPConnection.Close()
	defer AMQPChannel.Close()
}
