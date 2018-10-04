package main

import (
	"encoding/json"
	"log"
)

type Hub struct {
	clients map[*Client]bool
	broadcast chan []byte
	notification chan *ErpToSocketMessage
	register chan *Client
	unregister chan *Client
}

func hub() *Hub {
	return &Hub{
		broadcast:  make(chan []byte),
		notification:  make(chan *ErpToSocketMessage),
		register:   make(chan *Client),
		unregister: make(chan *Client),
		clients:    make(map[*Client]bool),
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
							log.Printf("Can`t encode chat message from appeal %s", message.AppealID)
						}

						select {
							case client.send <- byteMessage:
								log.Printf("Send message %s to client %s", message.AppealID, client.subscribe.AppealID)
							default:
								close(client.send)
								delete(h.clients, client)
							}
					}

				}
		}
	}
}