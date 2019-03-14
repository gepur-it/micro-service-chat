package main

import (
	"encoding/json"
	"github.com/sirupsen/logrus"
)

type Hub struct {
	clients      map[*Client]bool
	broadcast    chan []byte
	notification chan *ErpToSocketMessage
	register     chan *Client
	unregister   chan *Client
}

func hub() *Hub {
	return &Hub{
		broadcast:    make(chan []byte),
		notification: make(chan *ErpToSocketMessage),
		register:     make(chan *Client),
		unregister:   make(chan *Client),
		clients:      make(map[*Client]bool),
	}
}

func (h *Hub) run() {
	for {
		select {
		case client := <-h.register:
			h.clients[client] = true

		case client := <-h.unregister:
			if _, ok := h.clients[client]; ok {
				delete(h.clients, client)
				close(client.send)
			}

		case message := <-h.broadcast:
			for client := range h.clients {
				select {
				case client.send <- message:
				default:
					close(client.send)
					delete(h.clients, client)
				}
			}

		case message := <-h.notification:
			for client := range h.clients {
				if client.subscribe.AppealID == message.AppealID {
					byteMessage, err := json.Marshal(message.Message)

					if err != nil {
						logger.WithFields(logrus.Fields{
							"message": message.AppealID,
							"appeal":  client.subscribe.AppealID,
						}).Error("Can`t encode chat message from appeal:")
					}

					select {
					case client.send <- byteMessage:
						logger.WithFields(logrus.Fields{
							"message": message.AppealID,
							"appeal":  client.subscribe.AppealID,
						}).Info("Send message to client:")
					default:
						close(client.send)
						delete(h.clients, client)
					}
				}

			}
		}
	}
}

func (h *Hub) query() {
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

			h.notification <- erpToSocketMessage

			logger.WithFields(logrus.Fields{
				"AppealID": erpToSocketMessage.AppealID,
			}).Info("Read message from query:")

			d.Ack(false)
		}
	}()

	<-forever
}