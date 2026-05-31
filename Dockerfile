# syntax=docker/dockerfile:1

FROM golang:1.25-alpine AS build

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG VERSION=dev
RUN CGO_ENABLED=0 go build \
    -ldflags "-s -w -X main.agentVersion=${VERSION}" \
    -o /monitor-agent \
    ./cmd/main.go

# base images omit nvidia-smi; runtime includes cuda-command-line-tools.
FROM nvidia/cuda:12.6.0-runtime-ubuntu24.04 AS nvidia
RUN cp "$(command -v nvidia-smi)" /nvidia-smi

FROM alpine:3.21

# gcompat: run glibc nvidia-smi; use --gpus all so the NVIDIA Container Toolkit can inject host driver libs.
RUN apk add --no-cache ca-certificates docker-cli util-linux zfs gcompat

COPY --from=nvidia /nvidia-smi /usr/bin/nvidia-smi

COPY --from=build /monitor-agent /usr/local/bin/monitor-agent
COPY config-example.yml /etc/monitor-agent/config-example.yml

WORKDIR /etc/monitor-agent

ENTRYPOINT ["/usr/local/bin/monitor-agent"]
