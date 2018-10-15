package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/globalsign/mgo"
	"github.com/globalsign/mgo/bson"
	"github.com/sirupsen/logrus"
	"github.com/streadway/amqp"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/gorilla/websocket"
)

const (
	// Time allowed to write a message to the peer.
	writeWait = 10 * time.Second

	// Time allowed to read the next pong message from the peer.
	pongWait = 60 * time.Second

	// Send pings to peer with this period. Must be less than pongWait.
	pingPeriod = (pongWait * 9) / 10

	// Maximum message size allowed from peer.
	maxMessageSize = 512
)

var (
	newline = []byte{'\n'}
	space   = []byte{' '}
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

type Client struct {
	hub       *Hub
	conn      *websocket.Conn
	send      chan []byte
	subscribe Subscribe
}

type Subscribe struct {
	AppealID string `json:"appealId"`
	ApiKey   string `json:"apiKey"`
}

type SendMessageBack struct {
	ConversationId int    `json:"conversationId"`
	Message        string `json:"message"`
}

type User struct {
	ID           string    `json:"_id"`
	Username     string    `json:"username"`
	UserID       string    `json:"userId"`
	ObjectGUID   string    `json:"objectGUID"`
	LastActivity time.Time `json:"lastActivity"`
}

func (currentClient *Client) readPump() {
	defer func() {
		currentClient.hub.unregister <- currentClient
		currentClient.conn.Close()
	}()

	currentClient.conn.SetReadLimit(maxMessageSize)
	currentClient.conn.SetReadDeadline(time.Now().Add(pongWait))
	currentClient.conn.SetPongHandler(func(string) error { currentClient.conn.SetReadDeadline(time.Now().Add(pongWait)); return nil })

	for {
		_, message, err := currentClient.conn.ReadMessage()

		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				log.Printf("error: %v", err)
				logger.WithFields(logrus.Fields{
					"error": err,
					"addr":  currentClient.conn.RemoteAddr(),
				}).Error("Socket error:")
			}

			break
		}

		message = bytes.TrimSpace(bytes.Replace(message, newline, space, -1))

		logger.WithFields(logrus.Fields{
			"addr": currentClient.conn.RemoteAddr(),
		}).Info("Socket receive message:")

		if len(currentClient.subscribe.ApiKey) == 0 || len(currentClient.subscribe.AppealID) == 0 {
			subscribe := Subscribe{}
			err = json.Unmarshal(message, &subscribe)

			if err != nil || len(subscribe.ApiKey) == 0 || len(subscribe.AppealID) == 0 {
				logger.WithFields(logrus.Fields{
					"addr": currentClient.conn.RemoteAddr(),
				}).Warn("Hasta la vista, baby! Can`t decode subscribe message:")

				currentClient.hub.unregister <- currentClient

				logger.WithFields(logrus.Fields{
					"addr":   currentClient.conn.RemoteAddr(),
					"appeal": currentClient.subscribe.AppealID,
				}).Warn("Unregister current client:")

				break
			}

			url := fmt.Sprintf("mongodb://%s:%s@%s/%s", os.Getenv("MONGODB_USER"), os.Getenv("MONGODB_PASSWORD"), os.Getenv("MONGODB_HOST"), os.Getenv("MONGODB_DB"))
			mgoSession, err := mgo.Dial(url)
			failOnError(err, "Failed connect to mongo")
			mgoSession.SetMode(mgo.Monotonic, true)

			user := User{}
			connection := mgoSession.DB(os.Getenv("MONGODB_DB")).C("UserApiKey")

			err = connection.Find(bson.M{"_id": subscribe.ApiKey}).One(&user)

			if err != nil {
				logger.WithFields(logrus.Fields{
					"error":  err,
					"addr":   currentClient.conn.RemoteAddr(),
					"appeal": currentClient.subscribe.AppealID,
				}).Warn("Hasta la vista, baby! User %s not accept to connect this chat:")

				mgoSession.Close()
				break
			}

			currentClient.subscribe = subscribe
			mgoSession.Close()

			logger.WithFields(logrus.Fields{
				"addr":   currentClient.conn.RemoteAddr(),
				"appeal": currentClient.subscribe.AppealID,
			}).Info("Client subscribe to appeal:")

		} else {
			sendMessageBack := SendMessageBack{}
			err = json.Unmarshal(message, &sendMessageBack)

			if err != nil || len(sendMessageBack.Message) == 0 {

				logger.WithFields(logrus.Fields{
					"addr":   currentClient.conn.RemoteAddr(),
					"appeal": currentClient.subscribe.AppealID,
				}).Warn("Hasta la vista, baby! Wrong message:")

				currentClient.hub.unregister <- currentClient

				logger.WithFields(logrus.Fields{
					"addr":   currentClient.conn.RemoteAddr(),
					"appeal": currentClient.subscribe.AppealID,
				}).Warn("Unregister current client:")

				break
			}

			name := fmt.Sprintf("erp_send_message")

			query, err := AMQPChannel.QueueDeclare(
				name,
				true,
				false,
				false,
				false,
				nil,
			)

			failOnError(err, "Failed to declare a queue")

			err = AMQPChannel.Publish(
				"",
				query.Name,
				false,
				false,
				amqp.Publishing{
					DeliveryMode: amqp.Transient,
					ContentType:  "application/json",
					Body:         message,
					Timestamp:    time.Now(),
				})

			failOnError(err, "Failed to publish a message")

			logger.WithFields(logrus.Fields{
				"conversation": sendMessageBack.ConversationId,
				"addr":         currentClient.conn.RemoteAddr(),
			}).Info("Client send answer message to conversation:")
		}
	}
}

func (currentClient *Client) writePump() {
	ticker := time.NewTicker(pingPeriod)

	defer func() {
		ticker.Stop()
		currentClient.conn.Close()
	}()

	for {
		select {
		case message, ok := <-currentClient.send:
			currentClient.conn.SetWriteDeadline(time.Now().Add(writeWait))

			if !ok {
				// The hub closed the channel.
				currentClient.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}

			w, err := currentClient.conn.NextWriter(websocket.TextMessage)

			if err != nil {
				return
			}

			w.Write(message)

			// Add queued chat messages to the current websocket message.
			n := len(currentClient.send)

			for i := 0; i < n; i++ {
				w.Write(newline)
				w.Write(<-currentClient.send)
			}

			if err := w.Close(); err != nil {
				return
			}

			logger.WithFields(logrus.Fields{
				"addr":   currentClient.conn.RemoteAddr(),
				"appeal": currentClient.subscribe.AppealID,
			}).Info("Socket send message to client:")

		case <-ticker.C:
			currentClient.conn.SetWriteDeadline(time.Now().Add(writeWait))

			if err := currentClient.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}

		}
	}
}

func (currentClient *Client) query() {
	query, err := AMQPChannel.QueueDeclare(
		"erp_to_socket_message",
		true,
		false,
		false,
		false,
		nil,
	)
	failOnError(err, "Failed to declare a queue")

	msgs, err := AMQPChannel.Consume(
		query.Name,
		"",
		true,
		false,
		false,
		false,
		nil,
	)
	failOnError(err, "Failed to register a consumer")

	forever := make(chan bool)

	go func() {
		for d := range msgs {
			erpToSocketMessage := &ErpToSocketMessage{}
			err := json.Unmarshal(d.Body, &erpToSocketMessage)
			failOnError(err, "Can`t decode query callBack")

			currentClient.hub.notification <- erpToSocketMessage

			logger.WithFields(logrus.Fields{
				"message": erpToSocketMessage.Message.Message,
				"appeal":  erpToSocketMessage.AppealID,
			}).Info("Read message from query:")
		}
	}()

	<-forever
}
