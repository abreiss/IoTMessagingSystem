# IoT Messaging System

End-to-end encrypted messaging between Raspberry Pis using Go and Docker.

## Project Structure
- `cmd/` - Main application entry points for relay server and device clients



## Setup

```bash
go mod init github.com/yourusername/iotmessaging
go get github.com/gorilla/websocket
go get golang.org/x/crypto/hkdf
```

##
docker-compose up

## Build

```bash
make build
make up
```
