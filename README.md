# bitcoin-prometheus-exporter
Prometheus exporter for Bitcoin Core RPC.

## Configuration
Environment variables:

- `BITCOIN_RPC_SCHEME` (default: `http`)
- `BITCOIN_RPC_HOST` (default: `localhost`)
- `BITCOIN_RPC_PORT` (default: `8332`)
- `BITCOIN_RPC_USER`
- `BITCOIN_RPC_PASSWORD`
- `BITCOIN_CONF_PATH` (optional; if set, reads RPC settings from `bitcoin.conf`)
- `HASHPS_BLOCKS` (default: `-1,1,120`)
- `SMARTFEE_BLOCKS` (default: `2,3,5,20`)
- `METRICS_ADDR` (default: empty, binds all interfaces)
- `METRICS_PORT` (default: `9332`)
- `TIMEOUT` (default: `30`, seconds)
- `LOG_LEVEL` (default: `INFO`)
- `BAN_ADDRESS_METRICS` (default: `false`)
- `BAN_ADDRESS_LIMIT` (default: `100`)

## Run
### Local
```bash path=null start=null
go build -o bitcoin-prometheus-exporter .
./bitcoin-prometheus-exporter
```

Metrics endpoint:

```text path=null start=null
http://localhost:9332/metrics
```

### Docker
Image:

A pre-built Docker image is available on [Docker Hub](https://hub.docker.com/r/lnliz/bitcoin-prometheus-exporter).



Example:

```bash path=null start=null
docker run --rm -p 9332:9332 \
  -e BITCOIN_RPC_HOST=bitcoin-node \
  -e BITCOIN_RPC_PORT=8332 \
  -e BITCOIN_RPC_USER=user \
  -e BITCOIN_RPC_PASSWORD=pwd \
  lnliz/bitcoin-prometheus-exporter:latest
```
