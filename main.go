package main

import (
	"fmt"
	"github.com/joho/godotenv"
	"github.com/streadway/amqp"
	"log"
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
	AppealID string `json:"appealId"`
	Message  Message `json:"message"`
}

var AMQPConnection *amqp.Connection
var AMQPChannel *amqp.Channel

func failOnError(err error, msg string) {
	if err != nil {
		log.Fatalf("%s: %s", msg, err)
		panic(fmt.Sprintf("%s: %s", msg, err))
	}
}

func init() {
	err := godotenv.Load()
	failOnError(err, "Error loading .env file")

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
	log.Printf("Http connects: %s %s %s", r.RemoteAddr, r.Proto, r.Method)

	if r.URL.Path != "/" {
		http.Error(w, "Not found", http.StatusNotFound)
		return
	}

	if r.Method != "GET" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	http.ServeFile(w, r, "home.html")
}


func ws(hub *Hub, w http.ResponseWriter, r *http.Request) {
	log.Printf("Socket connects: %s %s %s", r.RemoteAddr, r.Proto, r.Method)

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

	log.Fatal(http.ListenAndServe(fmt.Sprintf(":%s", os.Getenv("PACT_LISTEN_PORT")), nil))

	defer AMQPConnection.Close()
	defer AMQPChannel.Close()
}