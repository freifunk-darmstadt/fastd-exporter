# fastd-exporter

This is a prometheus exporter for the
[fastd](https://github.com/NeoRaider/fastd) VPN daemon.

It queries information from fastd by relying on its
[status socket](https://fastd.readthedocs.io/en/stable/manual/config.html#main-configuration),
that exposes metadata in the JSON format.

We are happy to discuss the fastd-exporter with you on IRC and Matrix

- [#ffda](ircs://irc.hackint.org/ffda) on [hackint](https://hackint.org)
- [#ffda:hackint.org](https://matrix.to/#/!fRXrylqyzOnxPDpZMw:hackint.org?via=hackint.org&via=matrix.org&via=lossy.network)


## Install

Download the latest [release artifact](https://git.darmstadt.ccc.de/ffda/infra//fastd-exporter/-/jobs/artifacts/master/download?job=release) or build it yourself:

```console
go get git.darmstadt.ccc.de/ffda/infra/fastd-exporter
```

## Usage

The exporter supports multiple fastd instances, that must be passed as
arguments. Instances are currently expected to have their configuration
file at `/etc/fastd/<instance>/fastd.conf`.

```console
./fastd_exporter domain1 domain2 domain3
```

The exporter requires read access to both the `fastd.conf` and the
`status socket` that is configured within it.

Additional flags exist:

```console
Usage of ./fastd-exporter:
  -web.listen-address string
    	Address on which to expose metrics and web interface. (default ":9281")
  -web.telemetry-path string
    	Path under which to expose metrics. (default "/metrics")
```

By default the metrics webserver will listen on `:9281`, which can be
changed through the `--web.listen-address` parameter.

## Metrics

The exporter exposes both interface and peer metrics. Both include
various interface counters. Per peer metrics expose the name and
public key of the peer, as well as the interface its packets arrive on.
