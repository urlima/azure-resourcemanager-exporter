package main

import (
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armsubscriptions"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/security/armsecurity"
	"github.com/prometheus/client_golang/prometheus"
	log "github.com/sirupsen/logrus"
	prometheusCommon "github.com/webdevops/go-common/prometheus"
	"github.com/webdevops/go-common/prometheus/collector"
	"github.com/webdevops/go-common/utils/to"
)

type MetricsCollectorAzureRmSecurity struct {
	collector.Processor

	prometheus struct {
		securitycenterCompliance *prometheus.GaugeVec
		advisorRecommendations   *prometheus.GaugeVec
	}
}

func (m *MetricsCollectorAzureRmSecurity) Setup(collector *collector.Collector) {
	m.Processor.Setup(collector)

	m.prometheus.securitycenterCompliance = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "azurerm_securitycenter_compliance",
			Help: "Azure Audit SecurityCenter compliance status",
		},
		[]string{
			"subscriptionID",
			"assessmentType",
		},
	)
	prometheus.MustRegister(m.prometheus.securitycenterCompliance)

	m.prometheus.advisorRecommendations = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "azurerm_advisor_recommendation",
			Help: "Azure Audit Advisor recommendation",
		},
		[]string{
			"subscriptionID",
			"category",
			"resourceType",
			"resourceName",
			"resourceGroup",
			"problem",
			"impact",
			"risk",
		},
	)
	prometheus.MustRegister(m.prometheus.advisorRecommendations)
}

func (m *MetricsCollectorAzureRmSecurity) Reset() {
	m.prometheus.securitycenterCompliance.Reset()
	m.prometheus.advisorRecommendations.Reset()
}

func (m *MetricsCollectorAzureRmSecurity) Collect(callback chan<- func()) {
	err := AzureSubscriptionsIterator.ForEachAsync(m.Logger(), func(subscription *armsubscriptions.Subscription, logger *log.Entry) {
		m.collectAzureSecurityCompliance(subscription, logger, callback)
		// m.collectAzureAdvisorRecommendations(subscription, logger, callback)
	})
	if err != nil {
		m.Logger().Panic(err)
	}
}

func (m *MetricsCollectorAzureRmSecurity) collectAzureSecurityCompliance(subscription *armsubscriptions.Subscription, logger *log.Entry, callback chan<- func()) {
	client, err := armsecurity.NewCompliancesClient(AzureClient.GetCred(), nil)
	if err != nil {
		logger.Panic(err)
	}

	infoMetric := prometheusCommon.NewMetricsList()

	pager := client.NewListPager(*subscription.ID, nil)

	lastReportName := ""
	var lastReportTimestamp *time.Time
	for pager.More() {
		result, err := pager.NextPage(m.Context())
		if err != nil {
			logger.Panic(err)
		}

		if result.Value == nil {
			continue
		}

		for _, complienceReport := range result.Value {
			if lastReportTimestamp == nil || complienceReport.Properties.AssessmentTimestampUTCDate.UTC().After(*lastReportTimestamp) {
				timestamp := complienceReport.Properties.AssessmentTimestampUTCDate.UTC()
				lastReportTimestamp = &timestamp
				lastReportName = to.String(complienceReport.Name)
			}
		}
	}

	if lastReportName != "" {
		report, err := client.Get(m.Context(), *subscription.ID, lastReportName, nil)
		if err != nil {
			logger.Error(err)
			return
		}

		if report.Properties.AssessmentResult != nil {
			for _, result := range report.Properties.AssessmentResult {
				infoLabels := prometheus.Labels{
					"subscriptionID": stringPtrToStringLower(subscription.SubscriptionID),
					"assessmentType": stringPtrToStringLower(result.SegmentType),
				}
				infoMetric.Add(infoLabels, to.Float64(result.Percentage))
			}
		}
	}

	callback <- func() {
		infoMetric.GaugeSet(m.prometheus.securitycenterCompliance)
	}
}

//
// func (m *MetricsCollectorAzureRmSecurity) collectAzureAdvisorRecommendations(subscription *armsubscriptions.Subscription, logger *log.Entry, callback chan<- func()) {
// 	client := advisor.NewRecommendationsClientWithBaseURI(AzureClient.Environment.ResourceManagerEndpoint, *subscription.SubscriptionID)
// 	AzureClient.DecorateAzureAutorest(&client.Client)
//
// 	recommendationResult, err := client.ListComplete(m.Context(), "", nil, "")
// 	if err != nil {
// 		logger.Panic(err)
// 	}
//
// 	infoMetric := prometheusCommon.NewHashedMetricsList()
//
// 	for _, item := range *recommendationResult.Response().Value {
// 		resourceId := to.String(item.ID)
// 		azureResource, _ := azureCommon.ParseResourceId(resourceId)
//
// 		infoLabels := prometheus.Labels{
// 			"subscriptionID": stringPtrToStringLower(subscription.SubscriptionID),
// 			"category":       stringToStringLower(string(item.RecommendationProperties.Category)),
// 			"resourceType":   stringPtrToStringLower(item.RecommendationProperties.ImpactedField),
// 			"resourceName":   stringPtrToStringLower(item.RecommendationProperties.ImpactedValue),
// 			"resourceGroup":  azureResource.ResourceGroup,
// 			"problem":        to.String(item.RecommendationProperties.ShortDescription.Problem),
// 			"impact":         stringToStringLower(string(item.Impact)),
// 			"risk":           stringToStringLower(string(item.Risk)),
// 		}
//
// 		infoMetric.Inc(infoLabels)
// 	}
//
// 	callback <- func() {
// 		infoMetric.GaugeSet(m.prometheus.advisorRecommendations)
// 	}
// }
