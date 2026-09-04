# Install

## Homebrew

```sh
brew install --cask Allan-Nava/tap/galera-doctor
```

The cask lives in [Allan-Nava/homebrew-tap](https://github.com/Allan-Nava/homebrew-tap)
alongside the sibling tools, and `Allan-Nava/tap` is Homebrew's shorthand for
that repository — no `brew tap` step needed. It is generated and pushed by
goreleaser on every tag, from the checksums of the archives that tag uploaded,
so what Homebrew verifies is the bytes that were published.

macOS quarantines an unsigned binary, and a quarantined binary installs cleanly
and then refuses to run. The cask strips the attribute on install, and CI
asserts it worked — on Apple Silicon and on Intel, after every release and
again every Monday, because a release asset can be deleted long after the run
that made it went green.

Casks cover macOS and Linux. Everything else installs from an archive or the
container image below.

## A release archive

```sh
tag=v0.3.0
os=$(uname -s | tr 'A-Z' 'a-z'); arch=$(uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/')
curl -fsSLO "https://github.com/Allan-Nava/galera-doctor/releases/download/$tag/galera-doctor_${tag#v}_${os}_${arch}.tar.gz"
curl -fsSLO "https://github.com/Allan-Nava/galera-doctor/releases/download/$tag/SHA256SUMS"
shasum -a 256 -c SHA256SUMS --ignore-missing
tar xzf galera-doctor_${tag#v}_${os}_${arch}.tar.gz
./galera-doctor version
```

Every archive is built by the release workflow and carries a provenance
attestation, so a download can be checked against the run that produced it:

```sh
gh attestation verify galera-doctor_*.tar.gz --repo Allan-Nava/galera-doctor
```

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

## Container

```sh
docker run --rm ghcr.io/allan-nava/galera-doctor:latest checks
docker run --rm ghcr.io/allan-nava/galera-doctor:latest \
  audit --node "sg-01=audit:***@tcp(10.11.1.5:3306)/"
```

`linux/amd64` and `linux/arm64`, tagged with the version and `latest`, with the
same provenance attestation as the archives. Or build it yourself, which is the
same Dockerfile the workflow uses:

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
