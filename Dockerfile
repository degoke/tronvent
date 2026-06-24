FROM golang:1.26-alpine AS builder
RUN apk add --no-cache python3 make g++
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . ./
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /tronvent ./main.go

FROM alpine:3.19
RUN apk add --no-cache ca-certificates tzdata
RUN addgroup -S -g 1001 app && adduser -S -D -H -u 1001 -G app app
WORKDIR /app
COPY --from=builder /tronvent /app/tronvent
RUN chown 1001:1001 /app/tronvent
USER 1001:1001
EXPOSE 8080
ENTRYPOINT ["/app/tronvent"]
