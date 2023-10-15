# SPDX-FileCopyrightText: 2023 Christoph Mewes
# SPDX-License-Identifier: MIT

FROM alpine:3.17

RUN apk --no-cache add ca-certificates
COPY prow_exporter /usr/local/bin/

ENTRYPOINT ["prow_exporter"]
