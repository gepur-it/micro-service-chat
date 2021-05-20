FROM golang:latest
WORKDIR /app
COPY . /app

RUN go mod init gepur/chat-ws-client

RUN go get github.com/joho/godotenv
RUN go get github.com/streadway/amqp
RUN go get github.com/gorilla/websocket
RUN go get github.com/globalsign/mgo
RUN go get github.com/sirupsen/logrus
RUN go get github.com/zbindenren/logrus_mail

CMD ["go", "run", "."]

EXPOSE 80

