# syntax=docker/dockerfile:1
FROM golang:1.16.8-alpine3.14 AS builder

WORKDIR /app

COPY go.mod ./
COPY src/cloudflare-updater.go ./

RUN apk update && apk upgrade && apk add --no-cache ca-certificates
RUN update-ca-certificates
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o cloudflare-updater .

FROM scratch

COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /app/cloudflare-updater ./

CMD [ "./cloudflare-updater" ]