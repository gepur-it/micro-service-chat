FROM golang:latest
WORKDIR /app
COPY . /app
RUN go get github.com/joho/godotenv
RUN go get github.com/streadway/amqp
RUN go get github.com/gorilla/websocket
RUN go get github.com/globalsign/mgo
RUN go get github.com/sirupsen/logrus
CMD ["go", "run", "main.go", "hub.go", "client.go"]
EXPOSE 80

