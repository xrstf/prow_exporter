FROM golang:1.17-alpine as builder

WORKDIR /app/
COPY . .
RUN go build

FROM alpine:3.12

RUN apk --no-cache add ca-certificates
COPY --from=builder /app/prow_exporter .
EXPOSE 9855
ENTRYPOINT ["/prow_exporter"]
