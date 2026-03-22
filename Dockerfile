FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG COMMIT=unknown
RUN CGO_ENABLED=0 go build -ldflags "-X main.buildCommit=${COMMIT}" -o /bitcoin-prometheus-exporter .

FROM scratch
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=build /bitcoin-prometheus-exporter /bitcoin-prometheus-exporter
EXPOSE 9332
ENTRYPOINT ["/bitcoin-prometheus-exporter"]
