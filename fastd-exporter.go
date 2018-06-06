package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"regexp"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"strings"
)

var (
	address     = flag.String("web.listen-address", ":9281", "Address on which to expose metrics and web interface.")
	metricsPath = flag.String("web.telemetry-path", "/metrics", "Path under which to expose metrics.")
	instances   = flag.String("instances", "", "Fastd instances to report metrics on, comma separated.")
	peerMetrics = flag.Bool("metrics.perpeer", false, "Expose detailed metrics on each peer.")
)

// These are the structs necessary for unmarshalling the data that is being received on fastds unix socket.
type PacketStatistics struct {
	Count int `json:"packets"`
	Bytes int `json:"bytes"`
}

type Statistics struct {
	RX           PacketStatistics `json:"rx"`
	RX_Reordered PacketStatistics `json:"rx_reordered"`
	TX           PacketStatistics `json:"tx"`
	TX_Dropped   PacketStatistics `json:"tx_dropped"`
	TX_Error     PacketStatistics `json:"tx_error"`
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
	Connection *struct {
		Established float64    `json:"established"`
		Method      string     `json:"method"`
		Statistics  Statistics `json:"statistics"`
	} `json:"connection"`
	MAC []string `json:"mac_addresses"`
}

type PrometheusExporter struct {
	SocketName string

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

	peerUp     *prometheus.Desc
	peerUptime *prometheus.Desc

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

func c(parts ...string) string {
	parts = append([]string{"fastd"}, parts...)
	return strings.Join(parts, "_")
}

func NewPrometheusExporter(ifName string, sockName string) PrometheusExporter {
	// persistent labels
	l := prometheus.Labels{"interface": ifName}

	// dynamic labels
	p := []string{"public_key", "name"}

	return PrometheusExporter{
		SocketName: sockName,

		// global metrics
		up:     prometheus.NewDesc(c("up"), "whether the fastd process is up", nil, l),
		uptime: prometheus.NewDesc(c("uptime_seconds"), "uptime of the fastd process", nil, l),

		rxPackets:          prometheus.NewDesc(c("rx_packets"), "rx packet count", nil, l),
		rxBytes:            prometheus.NewDesc(c("rx_bytes"), "rx byte count", nil, l),
		rxReorderedPackets: prometheus.NewDesc(c("rx_reordered_packets"), "rx reordered packets count", nil, l),
		rxReorderedBytes:   prometheus.NewDesc(c("rx_reordered_bytes"), "rx reordered bytes count", nil, l),

		txPackets:        prometheus.NewDesc(c("tx_packets"), "tx packet count", nil, l),
		txBytes:          prometheus.NewDesc(c("tx_bytes"), "tx byte count", nil, l),
		txDroppedPackets: prometheus.NewDesc(c("tx_dropped_packets"), "tx dropped packets count", nil, l),
		txDroppedBytes:   prometheus.NewDesc(c("tx_dropped_bytes"), "tx dropped bytes count", nil, l),
		txErrorPackets:   prometheus.NewDesc(c("tx_error_packets"), "tx error packets count", nil, l),
		txErrorBytes:     prometheus.NewDesc(c("tx_error_bytes"), "tx error bytes count", nil, l),

		peersUpTotal:      prometheus.NewDesc(c("peer_up_total"), "number of connected peers", nil, l),

		// per peer metrics
		peerUp:     prometheus.NewDesc(c("peer_up"), "whether the peer is connected", p, l),
		peerUptime: prometheus.NewDesc(c("peer_uptime_seconds"), "peer session uptime", p, l),

		peerRxPackets:          prometheus.NewDesc(c("peer_rx_packets"), "peer rx packets count", p, l),
		peerRxBytes:            prometheus.NewDesc(c("peer_rx_bytes"), "peer rx bytes count", p, l),
		peerRxReorderedPackets: prometheus.NewDesc(c("peer_rx_reordered_packets"), "peer rx reordered packets count", p, l),
		peerRxReorderedBytes:   prometheus.NewDesc(c("peer_rx_reordered_bytes"), "peer rx reordered bytes count", p, l),

		peerTxPackets:        prometheus.NewDesc(c("peer_tx_packets"), "peer rx packet count", p, l),
		peerTxBytes:          prometheus.NewDesc(c("peer_tx_bytes"), "peer rx bytes count", p, l),
		peerTxDroppedPackets: prometheus.NewDesc(c("peer_tx_dropped_packets"), "peer tx dropped packets count", p, l),
		peerTxDroppedBytes:   prometheus.NewDesc(c("peer_tx_dropped_bytes"), "peer tx dropped bytes count", p, l),
		peerTxErrorPackets:   prometheus.NewDesc(c("peer_tx_error_packets"), "peer tx error packets count", p, l),
		peerTxErrorBytes:     prometheus.NewDesc(c("peer_tx_error_bytes"), "peer tx error bytes count", p, l),
	}
}

func (e PrometheusExporter) Describe(c chan<- *prometheus.Desc) {
	c <- e.up
	c <- e.uptime

	c <- e.rxPackets
	c <- e.rxBytes
	c <- e.rxReorderedPackets
	c <- e.rxReorderedBytes

	c <- e.txPackets
	c <- e.txBytes
	c <- e.txDroppedPackets
	c <- e.txDroppedBytes

	c <- e.peersUpTotal

	c <- e.peerUp
	c <- e.peerUptime

	c <- e.peerRxPackets
	c <- e.peerRxBytes
	c <- e.peerRxReorderedPackets
	c <- e.peerRxReorderedBytes

	c <- e.peerTxPackets
	c <- e.peerTxBytes
	c <- e.peerTxDroppedPackets
	c <- e.peerTxDroppedBytes
	c <- e.peerTxErrorPackets
	c <- e.peerTxErrorBytes
}

func (e PrometheusExporter) Collect(c chan<- prometheus.Metric) {
	data, err := data_from_sock(e.SocketName)
	if err != nil {
		log.Print(err)
		c <- prometheus.MustNewConstMetric(e.up, prometheus.GaugeValue, 0)
	} else {
		c <- prometheus.MustNewConstMetric(e.up, prometheus.GaugeValue, 1)
	}

	c <- prometheus.MustNewConstMetric(e.uptime, prometheus.GaugeValue, data.Uptime/1000)

	c <- prometheus.MustNewConstMetric(e.rxPackets, prometheus.CounterValue, float64(data.Statistics.RX.Count))
	c <- prometheus.MustNewConstMetric(e.rxBytes, prometheus.CounterValue, float64(data.Statistics.RX.Bytes))
	c <- prometheus.MustNewConstMetric(e.rxReorderedPackets, prometheus.CounterValue, float64(data.Statistics.RX_Reordered.Count))
	c <- prometheus.MustNewConstMetric(e.rxReorderedBytes, prometheus.CounterValue, float64(data.Statistics.RX_Reordered.Bytes))

	c <- prometheus.MustNewConstMetric(e.txPackets, prometheus.CounterValue, float64(data.Statistics.TX.Count))
	c <- prometheus.MustNewConstMetric(e.txBytes, prometheus.CounterValue, float64(data.Statistics.TX.Bytes))
	c <- prometheus.MustNewConstMetric(e.txDroppedPackets, prometheus.CounterValue, float64(data.Statistics.TX.Count))
	c <- prometheus.MustNewConstMetric(e.txDroppedBytes, prometheus.CounterValue, float64(data.Statistics.TX_Dropped.Bytes))

	peersUpTotal := 0

	for publicKey, peer := range data.Peers {
		if peer.Connection != nil {
			peersUpTotal += 1
		}

		if *peerMetrics {
			if peer.Connection == nil {
				c <- prometheus.MustNewConstMetric(e.peerUp, prometheus.GaugeValue, float64(0), publicKey, peer.Name)
			} else {
				c <- prometheus.MustNewConstMetric(e.peerUp, prometheus.GaugeValue, float64(1), publicKey, peer.Name)
				c <- prometheus.MustNewConstMetric(e.peerUptime, prometheus.GaugeValue, peer.Connection.Established/1000, publicKey, peer.Name)

				c <- prometheus.MustNewConstMetric(e.peerRxPackets, prometheus.CounterValue, float64(peer.Connection.Statistics.RX.Count), publicKey, peer.Name)
				c <- prometheus.MustNewConstMetric(e.peerRxBytes, prometheus.CounterValue, float64(peer.Connection.Statistics.RX.Bytes), publicKey, peer.Name)
				c <- prometheus.MustNewConstMetric(e.peerRxReorderedPackets, prometheus.CounterValue, float64(peer.Connection.Statistics.RX_Reordered.Count), publicKey, peer.Name)
				c <- prometheus.MustNewConstMetric(e.peerRxReorderedBytes, prometheus.CounterValue, float64(peer.Connection.Statistics.RX_Reordered.Bytes), publicKey, peer.Name)

				c <- prometheus.MustNewConstMetric(e.peerTxPackets, prometheus.CounterValue, float64(peer.Connection.Statistics.TX.Count), publicKey, peer.Name)
				c <- prometheus.MustNewConstMetric(e.peerTxBytes, prometheus.CounterValue, float64(peer.Connection.Statistics.TX.Bytes), publicKey, peer.Name)
				c <- prometheus.MustNewConstMetric(e.peerTxDroppedPackets, prometheus.CounterValue, float64(peer.Connection.Statistics.TX_Dropped.Count), publicKey, peer.Name)
				c <- prometheus.MustNewConstMetric(e.peerTxDroppedBytes, prometheus.CounterValue, float64(peer.Connection.Statistics.TX_Dropped.Bytes), publicKey, peer.Name)
				c <- prometheus.MustNewConstMetric(e.peerTxErrorPackets, prometheus.CounterValue, float64(peer.Connection.Statistics.TX_Error.Count), publicKey, peer.Name)
				c <- prometheus.MustNewConstMetric(e.peerTxErrorBytes, prometheus.CounterValue, float64(peer.Connection.Statistics.TX_Error.Bytes), publicKey, peer.Name)
			}
		}
	}

	c <- prometheus.MustNewConstMetric(e.peersUpTotal, prometheus.GaugeValue, float64(peersUpTotal))
}

func data_from_sock(sock string) (Message, error) {
	conn, err := net.DialTimeout("unix", sock, 2*time.Second)
	if err != nil {
		return Message{}, err
	}
	defer conn.Close()

	decoder := json.NewDecoder(conn)
	msg := Message{}
	err = decoder.Decode(&msg)
	if err != nil {
		return Message{}, err
	}

	return msg, nil
}

func config_from_instance(instance string) (string, string, error) {
	/*
	 * Parse fastds configuration and extract
	 *  a) interface     -> which is exposed as a label to identify the instance
	 *  b) status socket -> where we can extract status information for the instance
	 *
	 * Returns ifNamme, sockPath, err
	 * Errors when either configuration key is missing or the config file could not be read.
	 */
	data, err := ioutil.ReadFile(fmt.Sprintf("/etc/fastd/%s/fastd.conf", instance))
	if err != nil {
		return "", "", err
	}

	sockPath_pattern := regexp.MustCompile("status socket \"([^\"]+)\";")
	sockPath := sockPath_pattern.FindSubmatch(data)

	ifName_pattern := regexp.MustCompile("interface \"([^\"]+)\";")
	ifName := ifName_pattern.FindSubmatch(data)[1]

	if len(sockPath) > 1 && len(ifName) > 1 {
		return string(ifName), string(sockPath[1]), nil
	} else {
		return "", "", errors.New(fmt.Sprintf("Instance %s is missing one of ('status socket', 'interface') declaration.", instance))
	}
}

func main() {
	flag.Parse()

	if *instances == "" {
		log.Fatal("No instance given, exiting.")
	}

	instances := strings.Split(*instances, ",")

	for i := 0; i < len(instances); i++ {
		ifName, sockPath, err := config_from_instance(instances[i])
		if err != nil {
			log.Fatal(err)
		}
		log.Printf("Reading fastd data for %v from %v", ifName, sockPath)
		go prometheus.MustRegister(NewPrometheusExporter(ifName, sockPath))
	}

	// Expose the registered metrics via HTTP.
	http.Handle(*metricsPath, promhttp.Handler())
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<html>
		<head><title>fastd exporter</title></head>
		<body>
		<h1>fastd exporter</h1>
		<p><a href="` + *metricsPath + `">Metrics</a></p>
		</body>
		</html>`))
	})

	log.Fatal(http.ListenAndServe(*address, nil))
}
