package main

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"runtime"
	"strings"

	flags "github.com/jessevdk/go-flags"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	log "github.com/sirupsen/logrus"
	"github.com/webdevops/go-common/azuresdk/armclient"
	"github.com/webdevops/go-common/azuresdk/prometheus/tracing"
	"github.com/webdevops/go-common/msgraphsdk/msgraphclient"
	"github.com/webdevops/go-common/prometheus/collector"

	"github.com/webdevops/azure-resourcemanager-exporter/config"
)

const (
	Author    = "webdevops.io"
	UserAgent = "azure-resourcemanager-exporter/"
)

var (
	argparser *flags.Parser
	opts      config.Opts

	AzureClient                *armclient.ArmClient
	AzureSubscriptionsIterator *armclient.SubscriptionsIterator

	MsGraphClient *msgraphclient.MsGraphClient

	portscanPortRange []Portrange

	portrangeRegexp = regexp.MustCompile("^(?P<first>[0-9]+)(-(?P<last>[0-9]+))?$")

	// Git version information
	gitCommit = "<unknown>"
	gitTag    = "<unknown>"
)

type Portrange struct {
	FirstPort int
	LastPort  int
}

func main() {
	initArgparser()
	initLogger()

	log.Infof("starting azure-resourcemanager-exporter v%s (%s; %s; by %v)", gitTag, gitCommit, runtime.Version(), Author)
	log.Info(string(opts.GetJson()))

	log.Infof("init Azure connection")
	initAzureConnection()

	log.Infof("starting metrics collection")
	initMetricCollector()

	log.Infof("starting http server on %s", opts.Server.Bind)
	startHttpServer()
}

func initArgparser() {
	argparser = flags.NewParser(&opts, flags.Default)
	_, err := argparser.Parse()

	// check if there is an parse error
	if err != nil {
		var flagsErr *flags.Error
		if ok := errors.As(err, &flagsErr); ok && flagsErr.Type == flags.ErrHelp {
			os.Exit(0)
		} else {
			fmt.Println()
			argparser.WriteHelp(os.Stdout)
			os.Exit(1)
		}
	}

	if opts.Portscan.Enabled {
		// parse --portscan-range
		err := argparserParsePortrange()
		if err != nil {
			fmt.Fprintf(os.Stderr, "%v%v\n", "[ERROR] ", err.Error())
			fmt.Println()
			argparser.WriteHelp(os.Stdout)
			os.Exit(1)
		}
	}

	// deprecated option
	if len(opts.Azure.ResourceGroupTags) > 0 {
		opts.Azure.ResourceTags = opts.Azure.ResourceGroupTags
	}

	// scrape time
	if opts.Scrape.TimeGeneral == nil {
		opts.Scrape.TimeGeneral = &opts.Scrape.Time
	}

	if opts.Scrape.TimeResource == nil {
		opts.Scrape.TimeResource = &opts.Scrape.Time
	}

	if opts.Scrape.TimeQuota == nil {
		opts.Scrape.TimeQuota = &opts.Scrape.Time
	}

	if opts.Scrape.TimeCosts == nil {
		opts.Scrape.TimeCosts = &opts.Scrape.Time
	}

	if opts.Scrape.TimeIam == nil {
		opts.Scrape.TimeIam = &opts.Scrape.Time
	}

	if opts.Scrape.TimeSecurity == nil {
		opts.Scrape.TimeSecurity = &opts.Scrape.Time
	}

	if opts.Scrape.TimeResourceHealth == nil {
		opts.Scrape.TimeResourceHealth = &opts.Scrape.Time
	}

	if opts.Scrape.TimeGraph == nil {
		opts.Scrape.TimeGraph = &opts.Scrape.Time
	}

	if opts.Scrape.TimePortscan == nil {
		opts.Scrape.TimePortscan = &opts.Scrape.Time
	}

	if opts.Scrape.TimePortscan == nil || opts.Scrape.TimePortscan.Seconds() == 0 && opts.Portscan.Enabled {
		log.Fatalf(`portscan is enabled but has invalid scape time (zero)`)
	}

	// check deprecated env vars
	deprecatedEnvVars := map[string]string{
		"SCRAPE_TIME_CONTAINERREGISTRY": "not supported anymore",
		"SCRAPE_TIME_CONTAINERINSTANCE": "not supported anymore",
		"SCRAPE_TIME_EVENTHUB":          "not supported anymore",
		"SCRAPE_TIME_STORAGE":           "not supported anymore",
		"SCRAPE_TIME_COMPUTE":           "not supported anymore",
		"SCRAPE_TIME_NETWORK":           "not supported anymore",
		"SCRAPE_TIME_DATABASE":          "not supported anymore",
		"SCRAPE_TIME_COMPUTING":         "deprecated, please use SCRAPE_TIME_COMPUTE",
	}
	for envVar, reason := range deprecatedEnvVars {
		if os.Getenv(envVar) != "" {
			log.Fatalf("env var %v is %v", envVar, reason)
		}
	}
}

func initLogger() {
	// verbose level
	if opts.Logger.Debug {
		log.SetLevel(log.DebugLevel)
	}

	// trace level
	if opts.Logger.Trace {
		log.SetReportCaller(true)
		log.SetLevel(log.TraceLevel)
		log.SetFormatter(&log.TextFormatter{
			CallerPrettyfier: func(f *runtime.Frame) (string, string) {
				s := strings.Split(f.Function, "/")
				funcName := s[len(s)-1]
				return funcName, fmt.Sprintf("%s:%d", f.File, f.Line)
			},
		})
	}

	// json log format
	if opts.Logger.Json {
		log.SetReportCaller(true)
		log.SetFormatter(&log.JSONFormatter{
			DisableTimestamp: true,
			CallerPrettyfier: func(f *runtime.Frame) (string, string) {
				s := strings.Split(f.Function, "/")
				funcName := s[len(s)-1]
				return funcName, fmt.Sprintf("%s:%d", f.File, f.Line)
			},
		})
	}
}

func initAzureConnection() {
	var err error
	AzureClient, err = armclient.NewArmClientWithCloudName(*opts.Azure.Environment, log.StandardLogger())
	if err != nil {
		log.Panic(err.Error())
	}

	AzureClient.SetUserAgent(UserAgent + gitTag)

	// limit subscriptions (if filter is set)
	if len(opts.Azure.Subscription) >= 1 {
		AzureClient.SetSubscriptionFilter(opts.Azure.Subscription...)
	}

	AzureSubscriptionsIterator = armclient.NewSubscriptionIterator(AzureClient)
}

func initMsGraphConnection() {
	var err error
	if MsGraphClient == nil {
		MsGraphClient, err = msgraphclient.NewMsGraphClientWithCloudName(*opts.Azure.Environment, *opts.Azure.Tenant, log.StandardLogger())
		if err != nil {
			log.Panic(err.Error())
		}

		MsGraphClient.SetUserAgent(UserAgent + gitTag)
	}
}

func initMetricCollector() {
	var collectorName string

	collectorName = "General"
	if opts.Scrape.TimeGeneral.Seconds() > 0 {
		c := collector.New(collectorName, &MetricsCollectorAzureRmGeneral{}, log.StandardLogger())
		c.SetScapeTime(*opts.Scrape.TimeGeneral)
		if err := c.Start(); err != nil {
			log.Panic(err.Error())
		}
	} else {
		log.WithField("collector", collectorName).Infof("collector disabled")
	}

	collectorName = "Resource"
	if opts.Scrape.TimeResource.Seconds() > 0 {
		c := collector.New(collectorName, &MetricsCollectorAzureRmResources{}, log.StandardLogger())
		c.SetScapeTime(*opts.Scrape.TimeResource)
		if err := c.Start(); err != nil {
			log.Panic(err.Error())
		}
	} else {
		log.WithField("collector", collectorName).Infof("collector disabled")
	}

	collectorName = "Quota"
	if opts.Scrape.TimeQuota.Seconds() > 0 {
		c := collector.New(collectorName, &MetricsCollectorAzureRmQuota{}, log.StandardLogger())
		c.SetScapeTime(*opts.Scrape.TimeQuota)
		if err := c.Start(); err != nil {
			log.Panic(err.Error())
		}
	} else {
		log.WithField("collector", collectorName).Infof("collector disabled")
	}

	collectorName = "Costs"
	if opts.Scrape.TimeCosts.Seconds() > 0 {
		c := collector.New(collectorName, &MetricsCollectorAzureRmCosts{}, log.StandardLogger())
		c.SetScapeTime(*opts.Scrape.TimeCosts)
		if err := c.Start(); err != nil {
			log.Panic(err.Error())
		}
	} else {
		log.WithField("collector", collectorName).Infof("collector disabled")
	}

	collectorName = "Security"
	if opts.Scrape.TimeSecurity.Seconds() > 0 {
		c := collector.New(collectorName, &MetricsCollectorAzureRmSecurity{}, log.StandardLogger())
		c.SetScapeTime(*opts.Scrape.TimeSecurity)
		if err := c.Start(); err != nil {
			log.Panic(err.Error())
		}
	} else {
		log.WithField("collector", collectorName).Infof("collector disabled")
	}

	collectorName = "ResourceHealth"
	if opts.Scrape.TimeResourceHealth.Seconds() > 0 {
		c := collector.New(collectorName, &MetricsCollectorAzureRmHealth{}, log.StandardLogger())
		c.SetScapeTime(*opts.Scrape.TimeResourceHealth)
		if err := c.Start(); err != nil {
			log.Panic(err.Error())
		}
	} else {
		log.WithField("collector", collectorName).Infof("collector disabled")
	}

	collectorName = "IAM"
	if opts.Scrape.TimeIam.Seconds() > 0 {
		initMsGraphConnection()
		c := collector.New(collectorName, &MetricsCollectorAzureRmIam{}, log.StandardLogger())
		c.SetScapeTime(*opts.Scrape.TimeIam)
		if err := c.Start(); err != nil {
			log.Panic(err.Error())
		}
	} else {
		log.WithField("collector", collectorName).Infof("collector disabled")
	}

	collectorName = "GraphApps"
	if opts.Scrape.TimeGraph.Seconds() > 0 {
		initMsGraphConnection()
		c := collector.New(collectorName, &MetricsCollectorGraphApps{}, log.StandardLogger())
		c.SetScapeTime(*opts.Scrape.TimeGraph)
		if err := c.Start(); err != nil {
			log.Panic(err.Error())
		}
	} else {
		log.WithField("collector", collectorName).Infof("collector disabled")
	}

	collectorName = "Portscan"
	if opts.Portscan.Enabled && opts.Scrape.TimePortscan.Seconds() > 0 {
		c := collector.New(collectorName, &MetricsCollectorPortscanner{}, log.StandardLogger())
		c.SetScapeTime(*opts.Scrape.TimePortscan)
		if err := c.Start(); err != nil {
			log.Panic(err.Error())
		}
	} else {
		log.WithField("collector", collectorName).Infof("collector disabled")
	}
}

// start and handle prometheus handler
func startHttpServer() {
	mux := http.NewServeMux()

	// healthz
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		if _, err := fmt.Fprint(w, "Ok"); err != nil {
			log.Error(err)
		}
	})

	// readyz
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		if _, err := fmt.Fprint(w, "Ok"); err != nil {
			log.Error(err)
		}
	})

	mux.Handle("/metrics", collector.HttpWaitForRlock(
		tracing.RegisterAzureMetricAutoClean(promhttp.Handler())),
	)

	srv := &http.Server{
		Addr:         opts.Server.Bind,
		Handler:      mux,
		ReadTimeout:  opts.Server.ReadTimeout,
		WriteTimeout: opts.Server.WriteTimeout,
	}
	log.Fatal(srv.ListenAndServe())
}
