# syntax=docker/dockerfile:1
FROM golang:1.27.3-alpine AS build
WORKDIR /app
COPY go.mod ./
RUN go mod download
COPY *.go ./
RUN CGO_ENABLED=0 go build -trimpath -o /app/main

FROM alpine:3.23
RUN apk add --no-cache ca-certificates
WORKDIR /app
COPY --from=build /app/main ./main
COPY web ./web
USER 65532:65532
EXPOSE 8080
CMD ["/app/main"]
