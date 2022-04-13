package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"os"
	"regexp"
	"time"

	"strings"

	"github.com/ammario/ipisp/v2"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	configPathPattern = flag.String("config-path", "/etc/fastd/%s/fastd.conf", "Override fastd config path, %s will be replaced with the fastd instance name.")
	webListenAddress  = flag.String("web.listen-address", ":9281", "Address on which to expose metrics and web interface.")
	webMetricsPath    = flag.String("web.telemetry-path", "/metrics", "Path under which to expose metrics.")
)

// PacketStatistics These are the structs necessary for unmarshalling the data that is being received on fastds unix socket.
type PacketStatistics struct {
	Count int `json:"packets"`
	Bytes int `json:"bytes"`
}

type Statistics struct {
	Rx          PacketStatistics `json:"rx"`
	RxReordered PacketStatistics `json:"rx_reordered"`
	Tx          PacketStatistics `json:"tx"`
	TxDropped   PacketStatistics `json:"tx_dropped"`
	TxError     PacketStatistics `json:"tx_error"`
}

type Message struct {
	Uptime     float64         `json:"uptime"`
	Interface  string          `json:"interface"`
	Statistics Statistics      `json:"statistics"`
	Peers      map[string]Peer `json:"peers"`
}

type Peer struct {
	Name       string `json:"name"`
	Address    string `json:"address"`
	Interface  string `json:"interface"`
	Connection *struct {
		Established float64    `json:"established"`
		Method      string     `json:"method"`
		Statistics  Statistics `json:"statistics"`
	} `json:"connection"`
	MAC []string `json:"mac_addresses"`
}

type PrometheusExporter struct {
	statusSocketPath string

	up     *prometheus.Desc
	uptime *prometheus.Desc

	rxPackets *prometheus.Desc
	rxBytes   *prometheus.Desc

	rxReorderedPackets *prometheus.Desc
	rxReorderedBytes   *prometheus.Desc

	txPackets *prometheus.Desc
	txBytes   *prometheus.Desc

	txDroppedPackets *prometheus.Desc
	txDroppedBytes   *prometheus.Desc

	txErrorPackets *prometheus.Desc
	txErrorBytes   *prometheus.Desc

	peersUpTotal *prometheus.Desc

	peerUp           *prometheus.Desc
	peerUptime       *prometheus.Desc
	peerIpAddrFamily *prometheus.Desc
	peerAsn          *prometheus.Desc

	peerRxPackets          *prometheus.Desc
	peerRxBytes            *prometheus.Desc
	peerRxReorderedPackets *prometheus.Desc
	peerRxReorderedBytes   *prometheus.Desc

	peerTxPackets        *prometheus.Desc
	peerTxBytes          *prometheus.Desc
	peerTxDroppedPackets *prometheus.Desc
	peerTxDroppedBytes   *prometheus.Desc
	peerTxErrorPackets   *prometheus.Desc
	peerTxErrorBytes     *prometheus.Desc
}

func prefixWrapper(parts ...string) string {
	parts = append([]string{"fastd"}, parts...)
	return strings.Join(parts, "_")
}

func NewPrometheusExporter(instance string, sockName string) PrometheusExporter {
	staticLabels := prometheus.Labels{
		"fastd_instance": instance,
	}
	dynamicLabels := []string{
		"public_key",
		"name",
		"interface",
		"method",
	}

	return PrometheusExporter{
		statusSocketPath: sockName,

		// global metrics
		up:     prometheus.NewDesc(prefixWrapper("up"), "whether the fastd process is up", nil, staticLabels),
		uptime: prometheus.NewDesc(prefixWrapper("uptime_seconds"), "uptime of the fastd process", nil, staticLabels),

		rxPackets:          prometheus.NewDesc(prefixWrapper("rx_packets"), "rx packet count", nil, staticLabels),
		rxBytes:            prometheus.NewDesc(prefixWrapper("rx_bytes"), "rx byte count", nil, staticLabels),
		rxReorderedPackets: prometheus.NewDesc(prefixWrapper("rx_reordered_packets"), "rx reordered packets count", nil, staticLabels),
		rxReorderedBytes:   prometheus.NewDesc(prefixWrapper("rx_reordered_bytes"), "rx reordered bytes count", nil, staticLabels),

		txPackets:        prometheus.NewDesc(prefixWrapper("tx_packets"), "tx packet count", nil, staticLabels),
		txBytes:          prometheus.NewDesc(prefixWrapper("tx_bytes"), "tx byte count", nil, staticLabels),
		txDroppedPackets: prometheus.NewDesc(prefixWrapper("tx_dropped_packets"), "tx dropped packets count", nil, staticLabels),
		txDroppedBytes:   prometheus.NewDesc(prefixWrapper("tx_dropped_bytes"), "tx dropped bytes count", nil, staticLabels),
		txErrorPackets:   prometheus.NewDesc(prefixWrapper("tx_error_packets"), "tx error packets count", nil, staticLabels),
		txErrorBytes:     prometheus.NewDesc(prefixWrapper("tx_error_bytes"), "tx error bytes count", nil, staticLabels),

		peersUpTotal: prometheus.NewDesc(prefixWrapper("peers_up_total"), "number of connected peers", nil, staticLabels),

		// per peer metrics
		peerUp:           prometheus.NewDesc(prefixWrapper("peer_up"), "whether the peer is connected", dynamicLabels, staticLabels),
		peerUptime:       prometheus.NewDesc(prefixWrapper("peer_uptime_seconds"), "peer session uptime", dynamicLabels, staticLabels),
		peerIpAddrFamily: prometheus.NewDesc(prefixWrapper("peer_ipaddr_family"), "IP address family the peer is using to connect", dynamicLabels, staticLabels),
		peerAsn:          prometheus.NewDesc(prefixWrapper("peer_asn"), "ASN the peer is connecting from", dynamicLabels, staticLabels),

		peerRxPackets:          prometheus.NewDesc(prefixWrapper("peer_rx_packets"), "peer rx packets count", dynamicLabels, staticLabels),
		peerRxBytes:            prometheus.NewDesc(prefixWrapper("peer_rx_bytes"), "peer rx bytes count", dynamicLabels, staticLabels),
		peerRxReorderedPackets: prometheus.NewDesc(prefixWrapper("peer_rx_reordered_packets"), "peer rx reordered packets count", dynamicLabels, staticLabels),
		peerRxReorderedBytes:   prometheus.NewDesc(prefixWrapper("peer_rx_reordered_bytes"), "peer rx reordered bytes count", dynamicLabels, staticLabels),

		peerTxPackets:        prometheus.NewDesc(prefixWrapper("peer_tx_packets"), "peer rx packet count", dynamicLabels, staticLabels),
		peerTxBytes:          prometheus.NewDesc(prefixWrapper("peer_tx_bytes"), "peer rx bytes count", dynamicLabels, staticLabels),
		peerTxDroppedPackets: prometheus.NewDesc(prefixWrapper("peer_tx_dropped_packets"), "peer tx dropped packets count", dynamicLabels, staticLabels),
		peerTxDroppedBytes:   prometheus.NewDesc(prefixWrapper("peer_tx_dropped_bytes"), "peer tx dropped bytes count", dynamicLabels, staticLabels),
		peerTxErrorPackets:   prometheus.NewDesc(prefixWrapper("peer_tx_error_packets"), "peer tx error packets count", dynamicLabels, staticLabels),
		peerTxErrorBytes:     prometheus.NewDesc(prefixWrapper("peer_tx_error_bytes"), "peer tx error bytes count", dynamicLabels, staticLabels),
	}
}

func (exporter PrometheusExporter) Describe(channel chan<- *prometheus.Desc) {
	channel <- exporter.up
	channel <- exporter.uptime

	channel <- exporter.rxPackets
	channel <- exporter.rxBytes
	channel <- exporter.rxReorderedPackets
	channel <- exporter.rxReorderedBytes

	channel <- exporter.txPackets
	channel <- exporter.txBytes
	channel <- exporter.txDroppedPackets
	channel <- exporter.txDroppedBytes

	channel <- exporter.peersUpTotal

	channel <- exporter.peerUp
	channel <- exporter.peerUptime
	channel <- exporter.peerIpAddrFamily
	channel <- exporter.peerAsn

	channel <- exporter.peerRxPackets
	channel <- exporter.peerRxBytes
	channel <- exporter.peerRxReorderedPackets
	channel <- exporter.peerRxReorderedBytes

	channel <- exporter.peerTxPackets
	channel <- exporter.peerTxBytes
	channel <- exporter.peerTxDroppedPackets
	channel <- exporter.peerTxDroppedBytes
	channel <- exporter.peerTxErrorPackets
	channel <- exporter.peerTxErrorBytes
}

func (exporter PrometheusExporter) Collect(channel chan<- prometheus.Metric) {
	data, err := readFromStatusSocket(exporter.statusSocketPath)
	if err != nil {
		log.Print(err)
		channel <- prometheus.MustNewConstMetric(exporter.up, prometheus.GaugeValue, 0)
	} else {
		channel <- prometheus.MustNewConstMetric(exporter.up, prometheus.GaugeValue, 1)
	}

	channel <- prometheus.MustNewConstMetric(exporter.uptime, prometheus.GaugeValue, data.Uptime/1000)

	channel <- prometheus.MustNewConstMetric(exporter.rxPackets, prometheus.CounterValue, float64(data.Statistics.Rx.Count))
	channel <- prometheus.MustNewConstMetric(exporter.rxBytes, prometheus.CounterValue, float64(data.Statistics.Rx.Bytes))
	channel <- prometheus.MustNewConstMetric(exporter.rxReorderedPackets, prometheus.CounterValue, float64(data.Statistics.RxReordered.Count))
	channel <- prometheus.MustNewConstMetric(exporter.rxReorderedBytes, prometheus.CounterValue, float64(data.Statistics.RxReordered.Bytes))

	channel <- prometheus.MustNewConstMetric(exporter.txPackets, prometheus.CounterValue, float64(data.Statistics.Tx.Count))
	channel <- prometheus.MustNewConstMetric(exporter.txBytes, prometheus.CounterValue, float64(data.Statistics.Tx.Bytes))
	channel <- prometheus.MustNewConstMetric(exporter.txDroppedPackets, prometheus.CounterValue, float64(data.Statistics.Tx.Count))
	channel <- prometheus.MustNewConstMetric(exporter.txDroppedBytes, prometheus.CounterValue, float64(data.Statistics.TxDropped.Bytes))

	peersUpTotal := 0

	for publicKey, peer := range data.Peers {
		peerName := peer.Name
		interfaceName := data.Interface
		method := ""
		ipAddrFamily := 6

		if peer.Connection != nil {
			peersUpTotal += 1
			method = peer.Connection.Method
		}

		if interfaceName == "" {
			interfaceName = peer.Interface
		}

		peerIp, _, _ := net.SplitHostPort(peer.Address)
		if strings.Contains(peerIp, ".") {
			ipAddrFamily = 4
		}

		asnlookup, err := ipisp.LookupIP(context.Background(), net.ParseIP(peerIp))
		if err != nil {
			log.Print(err)
		}

		if peer.Connection == nil {
			channel <- prometheus.MustNewConstMetric(exporter.peerUp, prometheus.GaugeValue, float64(0), publicKey, peerName, interfaceName, method)
		} else {

			channel <- prometheus.MustNewConstMetric(exporter.peerUp, prometheus.GaugeValue, float64(1), publicKey, peerName, interfaceName, method)
			channel <- prometheus.MustNewConstMetric(exporter.peerUptime, prometheus.GaugeValue, peer.Connection.Established/1000, publicKey, peerName, interfaceName, method)
			channel <- prometheus.MustNewConstMetric(exporter.peerIpAddrFamily, prometheus.GaugeValue, float64(ipAddrFamily), publicKey, peerName, interfaceName, method)
			channel <- prometheus.MustNewConstMetric(exporter.peerAsn, prometheus.GaugeValue, float64(asnlookup.ASN), publicKey, peerName, interfaceName, method)

			channel <- prometheus.MustNewConstMetric(exporter.peerRxPackets, prometheus.CounterValue, float64(peer.Connection.Statistics.Rx.Count), publicKey, peerName, interfaceName, method)
			channel <- prometheus.MustNewConstMetric(exporter.peerRxBytes, prometheus.CounterValue, float64(peer.Connection.Statistics.Rx.Bytes), publicKey, peerName, interfaceName, method)
			channel <- prometheus.MustNewConstMetric(exporter.peerRxReorderedPackets, prometheus.CounterValue, float64(peer.Connection.Statistics.RxReordered.Count), publicKey, peerName, interfaceName, method)
			channel <- prometheus.MustNewConstMetric(exporter.peerRxReorderedBytes, prometheus.CounterValue, float64(peer.Connection.Statistics.RxReordered.Bytes), publicKey, peerName, interfaceName, method)

			channel <- prometheus.MustNewConstMetric(exporter.peerTxPackets, prometheus.CounterValue, float64(peer.Connection.Statistics.Tx.Count), publicKey, peerName, interfaceName, method)
			channel <- prometheus.MustNewConstMetric(exporter.peerTxBytes, prometheus.CounterValue, float64(peer.Connection.Statistics.Tx.Bytes), publicKey, peerName, interfaceName, method)
			channel <- prometheus.MustNewConstMetric(exporter.peerTxDroppedPackets, prometheus.CounterValue, float64(peer.Connection.Statistics.TxDropped.Count), publicKey, peerName, interfaceName, method)
			channel <- prometheus.MustNewConstMetric(exporter.peerTxDroppedBytes, prometheus.CounterValue, float64(peer.Connection.Statistics.TxDropped.Bytes), publicKey, peerName, interfaceName, method)
			channel <- prometheus.MustNewConstMetric(exporter.peerTxErrorPackets, prometheus.CounterValue, float64(peer.Connection.Statistics.TxError.Count), publicKey, peerName, interfaceName, method)
			channel <- prometheus.MustNewConstMetric(exporter.peerTxErrorBytes, prometheus.CounterValue, float64(peer.Connection.Statistics.TxError.Bytes), publicKey, peerName, interfaceName, method)
		}
	}

	channel <- prometheus.MustNewConstMetric(exporter.peersUpTotal, prometheus.GaugeValue, float64(peersUpTotal))
}

func readFromStatusSocket(sock string) (Message, error) {
	conn, err := net.DialTimeout("unix", sock, 2*time.Second)
	if err != nil {
		return Message{}, err
	}
	defer func(conn net.Conn) {
		_ = conn.Close()
	}(conn)

	decoder := json.NewDecoder(conn)
	msg := Message{}
	err = decoder.Decode(&msg)
	if err != nil {
		return Message{}, err
	}

	return msg, nil
}

type fastdConfig struct {
	statusSocketPath string
}

func parseConfig(instance string) (fastdConfig, error) {
	/*
	 * Parses a fastd configuration and extracts the status socket, where the exporter
	 * will pull metrics from.
	 *
	 * Returns statusSocketPath, err
	 * Errors when the configuration could not be read, no status socket is defined or the status socket does not exist
	 */
	data, err := ioutil.ReadFile(fmt.Sprintf(*configPathPattern, instance))
	if err != nil {
		return fastdConfig{}, err
	}

	statusSocketPattern := regexp.MustCompile("status socket \"([^\"]+)\";")
	match := statusSocketPattern.FindSubmatch(data)
	if len(match) == 0 {
		return fastdConfig{}, errors.New(fmt.Sprintf("Instance %s is missing 'status socket' declaration.", instance))
	}

	statusSocketPath := string(match[1])
	if _, err := os.Stat(statusSocketPath); err == nil {
		return fastdConfig{statusSocketPath}, nil
	} else {
		return fastdConfig{}, errors.New(fmt.Sprintf("Status socket at %s does not exist. Is the fastd instance %s up?.", statusSocketPath, instance))
	}
}

func main() {
	flag.Parse()

	instances := flag.Args()
	if len(instances) == 0 {
		log.Fatal("No instances specified, aborting.")
	}

	for i := 0; i < len(instances); i++ {
		config, err := parseConfig(instances[i])
		if err != nil {
			log.Fatal(err)
		}
		log.Printf("Reading fastd data for %v from %v", instances[i], config.statusSocketPath)
		go prometheus.MustRegister(NewPrometheusExporter(instances[i], config.statusSocketPath))
	}

	// Expose the registered metrics via HTTP.
	http.Handle(*webMetricsPath, promhttp.Handler())
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		_, err := w.Write([]byte(`<html>
				<head><title>fastd exporter</title></head>
				<body>
				<h1>fastd exporter</h1>
				<p><a href="` + *webMetricsPath + `">Metrics</a></p>
				</body>
				</html>`))
		if err != nil {
			return
		}
	})

	log.Fatal(http.ListenAndServe(*webListenAddress, nil))
}
