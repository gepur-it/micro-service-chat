package main

import (
	"bytes"
	"encoding/json"
	"github.com/globalsign/mgo"
	"github.com/globalsign/mgo/bson"
	"github.com/sirupsen/logrus"
	"github.com/streadway/amqp"
	"net/http"
	"os"
	"time"

	"github.com/gorilla/websocket"
)

const (
	writeWait      = 10 * time.Second
	pongWait       = 60 * time.Second
	pingPeriod     = (pongWait * 9) / 10
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

type SocketResponse struct {
	StatusMessage string `json:"statusMessage"`
	StatusCode    int    `json:"statusCode"`
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
				logger.WithFields(logrus.Fields{
					"error": err,
					"addr":  currentClient.conn.RemoteAddr(),
				}).Warn("Socket error:")
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

			mongoDBDialInfo := &mgo.DialInfo{
				Addrs:     []string{os.Getenv("MONGODB_HOST")},
				Username:  os.Getenv("MONGODB_USER"),
				Password:  os.Getenv("MONGODB_PASSWORD"),
				Database:  os.Getenv("MONGODB_DB"),
				Timeout:   60 * time.Second,
				Mechanism: "SCRAM-SHA-1",
			}

			mgoSession, err := mgo.DialWithInfo(mongoDBDialInfo)
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

				socketResponse := SocketResponse{StatusMessage: "fail", StatusCode: 401}

				bytesToSend, _ := json.Marshal(socketResponse)

				currentClient.send <- bytesToSend

				mgoSession.Close()

				break
			}

			socketResponse := SocketResponse{StatusMessage: "ok", StatusCode: 200}

			bytesToSend, err := json.Marshal(socketResponse)

			currentClient.send <- bytesToSend

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

			err = AMQPChannel.Publish(
				"",
				"erp_send_message",
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
	msgs, err := AMQPChannel.Consume(
		"erp_to_socket_message",
		"",
		false,
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
				"appeal": erpToSocketMessage.AppealID,
			}).Info("Read message from query:")

			d.Ack(false)
		}
	}()

	<-forever
}
