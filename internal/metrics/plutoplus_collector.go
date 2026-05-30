package metrics

import (
	"github.com/MattCheramie/GopherTrunk/internal/sdr/plutoplus"
	"github.com/prometheus/client_golang/prometheus"
)

type plutoCollector struct {
	reconnects *prometheus.Desc
	reconnectFailures *prometheus.Desc
	failures *prometheus.Desc
}

func newPlutoCollector() *plutoCollector {
	return &plutoCollector{
		reconnects: prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "plutoplus", "reconnects_total"),
			"Total successful plutoplus stream reconnects.",
			nil, nil,
		),
		reconnectFailures: prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "plutoplus", "reconnect_failures_total"),
			"Total failed plutoplus reconnect attempts.",
			nil, nil,
		),
		failures: prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "plutoplus", "failures_total"),
			"Total plutoplus transport failures by stage.",
			[]string{"stage"}, nil,
		),
	}
}

func (c *plutoCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.reconnects
	ch <- c.reconnectFailures
	ch <- c.failures
}

func (c *plutoCollector) Collect(ch chan<- prometheus.Metric) {
	s := plutoplus.RuntimeMetricsSnapshot()
	ch <- prometheus.MustNewConstMetric(c.reconnects, prometheus.CounterValue, float64(s.Reconnects))
	ch <- prometheus.MustNewConstMetric(c.reconnectFailures, prometheus.CounterValue, float64(s.ReconnectFailures))
	ch <- prometheus.MustNewConstMetric(c.failures, prometheus.CounterValue, float64(s.DialFailures), "dial")
	ch <- prometheus.MustNewConstMetric(c.failures, prometheus.CounterValue, float64(s.HandshakeFailures), "handshake")
	ch <- prometheus.MustNewConstMetric(c.failures, prometheus.CounterValue, float64(s.CommandFailures), "command")
	ch <- prometheus.MustNewConstMetric(c.failures, prometheus.CounterValue, float64(s.StreamFailures), "stream")
	ch <- prometheus.MustNewConstMetric(c.failures, prometheus.CounterValue, float64(s.UnknownFailures), "unknown")
}
