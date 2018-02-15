FROM golang:1.9.4-alpine3.7 AS build
ENV GOPATH /app
WORKDIR /app/src/github.com/cybertec-postgresql/vip-manager
COPY . .
RUN go install
FROM alpine:latest
RUN apk add --no-cache iproute2 dumb-init
COPY --from=build /app/bin/vip-manager /
ENTRYPOINT ["/usr/bin/dumb-init", "--", "/vip-manager"]
