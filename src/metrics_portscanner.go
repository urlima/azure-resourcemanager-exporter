package main

import (
	"time"
	"github.com/prometheus/client_golang/prometheus"
)

var (
	prometheusPublicIpPortscanStatus *prometheus.GaugeVec
	prometheusPublicIpPortscanUpdated *prometheus.GaugeVec
	prometheusPublicIpPortscanPort *prometheus.GaugeVec

	portscanner *Portscanner
)

func initMetricsPortscanner() {
	portscanner = &Portscanner{}
	portscanner.Reset()

	prometheusPublicIpPortscanStatus = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "azurerm_publicip_portscan_status",
			Help: "Azure ResourceManager public ip portscan status",
		},
		[]string{"ipAddress"},
	)

	prometheusPublicIpPortscanUpdated = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "azurerm_publicip_portscan_updated",
			Help: "Azure ResourceManager public ip portscan uptime timestamp",
		},
		[]string{"ipAddress"},
	)

	prometheusPublicIpPortscanPort = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "azurerm_publicip_portscan_port",
			Help: "Azure ResourceManager public ip port",
		},
		[]string{"ipAddress", "protocol", "port", "description"},
	)

	prometheus.MustRegister(prometheusPublicIpPortscanStatus)
	prometheus.MustRegister(prometheusPublicIpPortscanUpdated)
	prometheus.MustRegister(prometheusPublicIpPortscanPort)


	portscanner.Callbacks.FinishScan = func(c *Portscanner) {
		Logger.Messsage("Finished portscan for %v IPs", len(portscanner.PublicIps))
	}

	portscanner.Callbacks.StartupScan = func(c *Portscanner) {
		Logger.Messsage(
			"Starting portscan for %v IPs (parallel:%v, threads per run:%v, timeout:%vs, portranges:%v)",
			len(c.PublicIps),
			opts.PortscanPrallel,
			opts.PortscanThreads,
			opts.PortscanTimeout,
			opts.portscanPortRange,
		)
	}

	portscanner.Callbacks.StartScanIpAdress = func(c *Portscanner, ipAddress string) {
		Logger.Messsage("Start port scanning for %v", ipAddress)

		prometheusPublicIpPortscanStatus.With(prometheus.Labels{
			"ipAddress": ipAddress,
		}).Set(0)
	}

	portscanner.Callbacks.FinishScanIpAdress = func(c *Portscanner, ipAddress string) {
		prometheusPublicIpPortscanStatus.With(prometheus.Labels{
			"ipAddress": ipAddress,
		}).Set(1)

		prometheusPublicIpPortscanUpdated.With(prometheus.Labels{
			"ipAddress": ipAddress,
		}).Set(float64(time.Now().Unix()))
	}

	portscanner.Callbacks.ResultCleanup = func(c *Portscanner) {
		prometheusPublicIpPortscanPort.Reset()
	}

	portscanner.Callbacks.ResultPush = func(c *Portscanner, result PortscannerResult) {
		prometheusPublicIpPortscanPort.With(result.Labels).Set(result.Value)
	}

	firstStart := true
	go func() {
		for {
			if len(portscanner.PublicIps) > 0 {
				portscanner.Start()
				time.Sleep(time.Duration(opts.PortscanTime) * time.Second)
			} else {
				if firstStart {
					// short delayed first time start
					time.Sleep(time.Duration(10) * time.Second)
				} else {
					// longer delayed restart
					time.Sleep(time.Duration(opts.ScrapeTime + 5) * time.Second)
				}
			}
		}
	}()
}
