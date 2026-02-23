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

## Sending custom messages from each device container

The device client already reads from stdin. With `stdin_open` + `tty` enabled in `docker-compose.yaml`, you can attach to each running container and type messages.

1) Start the stack:

```bash
docker compose up --build
```

2) In a second terminal, attach to device A

```bash
docker attach iot-device-a
```

3) In a third terminal, attach to device B

```bash
docker attach iot-device-b
```

Notes:
- You may need to hit enter if theres no prompt to type right away.
- To detach without stopping the container, use `Ctrl-p` then `Ctrl-q`.
- Type `quit` to gracefully stop a device client.


