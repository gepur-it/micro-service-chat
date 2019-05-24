package main

import (
	"fmt"
	"github.com/joho/godotenv"
	"github.com/sirupsen/logrus"
	"github.com/streadway/amqp"
	"github.com/zbindenren/logrus_mail"
	"net/http"
	"os"
	"strconv"
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

func getenvInt(key string) (int, error) {
	s := os.Getenv(key)
	v, err := strconv.Atoi(s)

	if err != nil {
		return 0, err
	}

	return v, nil
}

func init() {
	err := godotenv.Load()

	if err != nil {
		panic(fmt.Sprintf("%s: %s", "Error loading .env file", err))
	}

	port, err := getenvInt("LOGTOEMAIL_SMTP_PORT")

	if err != nil {
		panic(fmt.Sprintf("%s: %s", "Error read smtp port from env", err))
	}

	hook, err := logrus_mail.NewMailAuthHook(
		os.Getenv("LOGTOEMAIL_APP_NAME"),
		os.Getenv("LOGTOEMAIL_SMTP_HOST"),
		port,
		os.Getenv("LOGTOEMAIL_SMTP_FROM"),
		os.Getenv("LOGTOEMAIL_SMTP_TO"),
		os.Getenv("LOGTOEMAIL_SMTP_USERNAME"),
		os.Getenv("LOGTOEMAIL_SMTP_PASSWORD"),
	)

	logger.SetLevel(logrus.DebugLevel)
	logger.SetOutput(os.Stdout)
	logger.SetFormatter(&logrus.TextFormatter{})

	logger.Hooks.Add(hook)

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

	logger.WithFields(logrus.Fields{}).Info("Server init:")
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
	logger.WithFields(logrus.Fields{}).Info("Server starting:")
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
	logger.WithFields(logrus.Fields{}).Info("Server stopped:")
}
