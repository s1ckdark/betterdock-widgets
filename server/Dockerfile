FROM golang:1.27.1-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /market-api ./cmd/market-api
FROM alpine:3.23
RUN apk add --no-cache ca-certificates && addgroup -S market && adduser -S -G market market
COPY --from=build /market-api /usr/local/bin/market-api
USER market
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/market-api"]
