# syntax=docker/dockerfile:1.4
FROM cgr.dev/chainguard/wolfi-base AS build
LABEL maintainer="Will Norris <will@willnorris.com>"

RUN apk update && apk add build-base git openssh go-1.24

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG TARGETOS
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -v ./cmd/imageproxy

FROM cgr.dev/chainguard/static:latest

COPY --from=build /app/imageproxy /app/imageproxy

ENTRYPOINT ["/app/imageproxy"]

EXPOSE 8080
