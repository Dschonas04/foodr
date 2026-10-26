FROM golang:1.27-alpine AS bau
WORKDIR /quelle
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o /foodr .

FROM alpine:3.24
RUN apk add --no-cache ca-certificates tzdata && adduser -D -u 10001 foodr
ENV TZ=Europe/Berlin
USER foodr
COPY --from=bau /foodr /usr/local/bin/foodr
EXPOSE 8300
ENTRYPOINT ["/usr/local/bin/foodr"]
