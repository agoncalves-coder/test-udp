# syntax=docker/dockerfile:1
FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/server ./cmd/server

FROM alpine:3.22
# ffmpeg: decode de frames H.264 del experimento E8 (PRD §6) vía exec.Command
RUN apk add --no-cache ffmpeg ca-certificates
COPY --from=build /out/server /usr/local/bin/server
ENV HTTP_PORT=8080 UDP_PORT=5004 DATA_DIR=/data
EXPOSE 8080/tcp 5004/udp
ENTRYPOINT ["/usr/local/bin/server"]
