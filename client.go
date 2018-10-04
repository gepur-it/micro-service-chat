package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/streadway/amqp"
	"log"
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
}

type Client struct {
	hub *Hub
	conn *websocket.Conn
	send chan []byte
	subscribe Subscribe
}

type Subscribe struct {
	AppealID string `json:"appealId"`
	ApiKey string `json:"apiKey"`
}

type SendMessageBack struct {
	ConversationId int `json:"conversationId"`
	Message string `json:"message"`
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
			}

			break
		}

		message = bytes.TrimSpace(bytes.Replace(message, newline, space, -1))
		log.Printf("Socket recieve message: %s from %s", message, currentClient.conn.RemoteAddr())

		if len(currentClient.subscribe.ApiKey) == 0 || len(currentClient.subscribe.AppealID) == 0 {
			subscribe := Subscribe{}
			err = json.Unmarshal(message, &subscribe)

			if err != nil || len(subscribe.ApiKey) == 0 || len(subscribe.AppealID) == 0 {
				log.Printf("Hasta la vista, baby! Can`t decode subscribe message: %s from %s", message, currentClient.conn.RemoteAddr())
				currentClient.hub.unregister <- currentClient

				break
			}

			currentClient.subscribe = subscribe
			log.Printf("Client %s subscribe to appeal: %s", currentClient.subscribe.AppealID, currentClient.conn.RemoteAddr())
		} else {
			sendMessageBack := SendMessageBack{}
			err = json.Unmarshal(message, &sendMessageBack)

			if err != nil || len(sendMessageBack.Message) == 0 {
				log.Printf("Hasta la vista, baby! Wrong message: %s from %s", message, currentClient.conn.RemoteAddr())
				currentClient.hub.unregister <- currentClient
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

			log.Printf("Anwser message %s to conversation %s", sendMessageBack.Message, sendMessageBack.ConversationId)
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

				log.Printf("Socket send message: %s from %s", message, currentClient.conn.RemoteAddr())


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

			log.Printf("Read from query message %s for appeal %s",erpToSocketMessage.Message.Message, erpToSocketMessage.AppealID)
		}
	}()

	<-forever
}