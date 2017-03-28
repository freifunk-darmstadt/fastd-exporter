# fastd-exporter

We are building a prometheus exporter for the fastd vpn daemon. We don't have a working version yet, stay tuned

## Formatting

We are using `go fmt` for formatting the code.

## Getting Started

For now run

```
$ go build
```

and

```
$ ./fastd-exporter --instance ffda
```

It will print json data received from the socket and maybe also show metrics on `http://127.0.0.1:9099/metrics`.
