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
	address     = flag.String("web.listen-address", ":9099", "Address on which to expose metrics and web interface.")
	metricsPath = flag.String("web.telemetry-path", "/metrics", "Path under which to expose metrics.")
	instances   = flag.String("instances", "", "The fastd instances to Update on, comma separated.")
)

// These are the structs necessary for unmarshalling the data that is being
// received on fastds unix socket.
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
		Established time.Duration `json:"established"`
		Method      string        `json:"method"`
		Statistics  Statistics    `json:"statistics"`
	} `json:"connection"`
	MAC []string `json:"mac_addresses"`
}

type PrometheusExporter struct {
	SocketName string

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
}

func c(parts ...string) string {
	parts = append([]string{"fastd"}, parts...)
	return strings.Join(parts, "_")
}

func NewPrometheusExporter(ifName string, sockName string) PrometheusExporter {
	l := prometheus.Labels{"interface": ifName}

	return PrometheusExporter{
		SocketName: sockName,
		uptime:     prometheus.NewDesc(c("uptime"), "uptime of the prometheus exporter", nil, l),

		rxPackets:          prometheus.NewDesc(c("rx_packets"), "rx packet count", nil, l),
		rxBytes:            prometheus.NewDesc(c("rx_bytes"), "rx byte count", nil, l),
		rxReorderedPackets: prometheus.NewDesc(c("rx_reordered_packets"), "rx reordered packets", nil, l),
		rxReorderedBytes:   prometheus.NewDesc(c("rx_reordered_bytes"), "rx reordered packets", nil, l),

		txPackets:        prometheus.NewDesc(c("tx_packets"), "tx packet count", nil, l),
		txBytes:          prometheus.NewDesc(c("tx_bytes"), "tx byte count", nil, l),
		txDroppedPackets: prometheus.NewDesc(c("tx_dropped_packets"), "tx dropped packets", nil, l),
		txDroppedBytes:   prometheus.NewDesc(c("tx_dropped_bytes"), "tx dropped packets", nil, l),
	}
}

func (e PrometheusExporter) Describe(c chan<- *prometheus.Desc) {
	c <- e.uptime

	c <- e.rxPackets
	c <- e.rxBytes
	c <- e.rxReorderedPackets
	c <- e.rxReorderedBytes

	c <- e.txPackets
	c <- e.txBytes
	c <- e.txDroppedPackets
	c <- e.txDroppedBytes
}

func (e PrometheusExporter) Collect(c chan<- prometheus.Metric) {
	data := data_from_sock(e.SocketName)

	c <- prometheus.MustNewConstMetric(e.uptime, prometheus.GaugeValue, data.Uptime)

	c <- prometheus.MustNewConstMetric(e.rxPackets, prometheus.CounterValue, float64(data.Statistics.RX.Count))
	c <- prometheus.MustNewConstMetric(e.rxBytes, prometheus.CounterValue, float64(data.Statistics.RX.Bytes))
	c <- prometheus.MustNewConstMetric(e.rxReorderedPackets, prometheus.CounterValue, float64(data.Statistics.RX_Reordered.Count))
	c <- prometheus.MustNewConstMetric(e.rxReorderedBytes, prometheus.CounterValue, float64(data.Statistics.RX_Reordered.Bytes))

	c <- prometheus.MustNewConstMetric(e.txPackets, prometheus.CounterValue, float64(data.Statistics.TX.Count))
	c <- prometheus.MustNewConstMetric(e.txBytes, prometheus.CounterValue, float64(data.Statistics.TX.Bytes))
	c <- prometheus.MustNewConstMetric(e.txDroppedPackets, prometheus.CounterValue, float64(data.Statistics.TX.Count))
	c <- prometheus.MustNewConstMetric(e.txDroppedBytes, prometheus.CounterValue, float64(data.Statistics.TX_Dropped.Bytes))
}

func data_from_sock(sock string) Message {
	conn, err := net.DialTimeout("unix", sock, 2*time.Second)
	if err != nil {
		log.Fatal(err)
	}
	defer conn.Close()

	decoder := json.NewDecoder(conn)
	msg := Message{}
	err = decoder.Decode(&msg)
	if err != nil {
		log.Fatal(err)
	}

	dat, err := json.MarshalIndent(msg, "", "\t")
	if err != nil {
		log.Fatal(err)
	}

	log.Print(string(dat))
	return msg
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
