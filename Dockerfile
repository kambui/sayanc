# syntax=docker/dockerfile:1
FROM golang:1.16-alpine
WORKDIR /app
COPY web /app/web
COPY go.mod ./
RUN go mod download
COPY *.go ./
RUN go build -o /app/main
EXPOSE 8080
CMD ["/app/main"]