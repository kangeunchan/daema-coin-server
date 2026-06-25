FROM golang:1.26-alpine AS builder

WORKDIR /src

RUN apk add --no-cache ca-certificates

COPY go.mod go.sum ./
RUN go mod download

COPY cmd ./cmd
COPY internal ./internal

RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o /out/daema-coin-server ./cmd/server

FROM alpine:3.22

RUN apk add --no-cache ca-certificates tzdata \
	&& addgroup -S daema \
	&& adduser -S -G daema daema

WORKDIR /app

COPY --from=builder /out/daema-coin-server /app/daema-coin-server

USER daema

EXPOSE 8080

ENTRYPOINT ["/app/daema-coin-server"]
