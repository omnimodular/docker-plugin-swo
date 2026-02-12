FROM alpine:3.19
RUN apk add --no-cache ca-certificates
COPY output/docker-swo-log-driver /usr/bin/
