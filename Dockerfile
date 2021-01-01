FROM golang:1.16-rc-alpine

WORKDIR /app

COPY go.* main.go ./

RUN go mod download

RUN go build -o huetemp

CMD ["./huetemp"]
