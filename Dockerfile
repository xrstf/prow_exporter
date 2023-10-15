# SPDX-FileCopyrightText: 2023 Christoph Mewes
# SPDX-License-Identifier: MIT

FROM golang:1.21-alpine as builder

WORKDIR /app/
COPY . .
RUN apk add -U make git && make

FROM alpine:3.17

RUN apk --no-cache add ca-certificates
COPY --from=builder /app/_build/prow_exporter /usr/local/bin/
EXPOSE 9855
USER nobody
ENTRYPOINT ["prow_exporter"]
