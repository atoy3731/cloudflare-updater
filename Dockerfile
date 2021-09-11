# syntax=docker/dockerfile:1
FROM golang:1.16 AS builder

WORKDIR /app

COPY go.mod ./
COPY src/cloudflare-updater.go ./

RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o cloudflare-updater .

FROM scratch

COPY --from=builder /app/cloudflare-updater ./

CMD [ "./cloudflare-updater" ]