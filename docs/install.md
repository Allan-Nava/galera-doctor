# Install

## Go

```sh
go install github.com/Allan-Nava/galera-doctor/cmd/galera-doctor@latest
```

Go 1.25 or newer. One dependency: the MySQL driver.

## From source

```sh
git clone https://github.com/Allan-Nava/galera-doctor
cd galera-doctor
go build -o galera-doctor ./cmd/galera-doctor
./galera-doctor checks
```

## Docker

```sh
docker build -t galera-doctor .
docker run --rm galera-doctor checks
```

The image is `scratch` plus the binary. If your DSNs use TLS, mount a CA bundle
— the image carries none, because most deployments audit over a private network.

## What it needs

- TCP to each node's MySQL port, and to the ProxySQL admin port if you use
  `--proxysql`.
- An audit user with `USAGE, PROCESS, SELECT` — see
  [permissions and safety](safety.md).
- A writable path for `--state`, if you want rates instead of lifetime totals.
