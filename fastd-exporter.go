package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"regexp"
	"strings"
	"time"
	"errors"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	address   = flag.String("listen-address", ":9099", "The address to listen on for HTTP requests.")
	instances = flag.String("instances", "", "The fastd instances to Update on, comma separated.")
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
	Uptime     float64	   `json:"uptime"`
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


func init() {
	// register metrics
}

func Update(sock string) {
	data := data_from_sock(sock)

	// TODO: continue here mapping json data to metrics
	// prometheus.MustNewConstMetric(, , data.Uptime)
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

func sock_from_instance(instance string) (string, error) {
	data, err := ioutil.ReadFile(fmt.Sprintf("/etc/fastd/%s/fastd.conf", instance))
	if err != nil {
		return "", err
	}

	pattern := regexp.MustCompile("status socket \"([^\"]+)\";")
	matches := pattern.FindSubmatch(data)

	if len(matches) > 1 {
		return string(matches[1]), nil
	} else {
		return "", errors.New(fmt.Sprintf("Instance %s has no status socket configured.", instance))
	}
}

func main() {
	flag.Parse()

	if *instances == "" {
		log.Fatal("No instance given, exiting.")
	}

	instances := strings.Split(*instances, ",")
	for i := 0; i < len(instances); i++ {
		sock, err := sock_from_instance(instances[i])
		if err != nil {
			log.Fatal(err)
		}
		go func() {
			for {
				Update(sock)
				time.Sleep(time.Duration(15 * time.Second))
			}
		}()
	}

	// Expose the registered metrics via HTTP.
	http.Handle("/metrics", promhttp.Handler())
	log.Fatal(http.ListenAndServe(*address, nil))
}
