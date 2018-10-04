FROM golang:latest
WORKDIR /app
COPY . /app
RUN go get github.com/joho/godotenv
RUN go get github.com/streadway/amqp
RUN go get github.com/gorilla/websocket

CMD ["go", "run", "main.go", "hub.go", "client.go"]
EXPOSE 80

