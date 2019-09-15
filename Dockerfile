FROM golang:1.13.0-alpine3.10 AS builder
WORKDIR /app
COPY . .
RUN GOARCH=amd64 GOOS=linux CGO_ENABLED=0 go build -v cmd/server.go

FROM scratch
COPY --from=builder /app/server .
COPY --from=builder /app/cmd/dummy_cert/server.key .
COPY --from=builder /app/cmd/dummy_cert/server.pem .
ENTRYPOINT ["/server"]
CMD ["-v=2"]