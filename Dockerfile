# A static binary on scratch. galera-doctor speaks the MySQL protocol to
# addresses it is given and does nothing else: no DNS-over-TLS, no CA bundle
# needed unless the DSN asks for TLS — add one with a volume if it does. There
# is no shell in the image, which is one less thing to reason about when it is
# pointed at a production cluster.
FROM golang:1.25-alpine AS build
ARG VERSION=dev
WORKDIR /src
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /galera-doctor ./cmd/galera-doctor

FROM scratch
COPY --from=build /galera-doctor /galera-doctor
USER 65534:65534
ENTRYPOINT ["/galera-doctor"]
CMD ["help"]
