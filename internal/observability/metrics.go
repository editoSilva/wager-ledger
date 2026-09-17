package observability

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

type Metrics struct {
	WagerTransactionsTotal       *prometheus.CounterVec
	IdempotentReplaysTotal       prometheus.Counter
	OptimisticLockRetriesTotal   prometheus.Counter
	TooManyRetriesTotal          prometheus.Counter
	OutboxPublishLatencySeconds  prometheus.Histogram
	OutboxPublishFailuresTotal   prometheus.Counter
	MessageProcessingSeconds     *prometheus.HistogramVec
	InboxDuplicatesTotal         prometheus.Counter
	MessageDeliveryFailuresTotal prometheus.Counter
	ReconciliationChecksTotal    prometheus.Counter
	ReconciliationDivergentTotal prometheus.Counter

	registry *prometheus.Registry
}

func NewMetrics() *Metrics {
	registry := prometheus.NewRegistry()

	m := &Metrics{
		WagerTransactionsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "wager_transactions_total",
			Help: "Total de transações de aposta processadas, por resultado e tipo.",
		}, []string{"status", "kind"}),
		IdempotentReplaysTotal: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "wager_idempotent_replays_total",
			Help: "Total de requisições respondidas via replay idempotente.",
		}),
		OptimisticLockRetriesTotal: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "wager_optimistic_lock_retries_total",
			Help: "Total de tentativas repetidas por conflito de versão otimista na carteira.",
		}),
		TooManyRetriesTotal: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "wager_too_many_retries_total",
			Help: "Total de operações que esgotaram as tentativas de retry por disputa de concorrência.",
		}),
		OutboxPublishLatencySeconds: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "outbox_publish_latency_seconds",
			Help:    "Atraso entre a ocorrência do evento e sua publicação bem-sucedida na fila.",
			Buckets: prometheus.DefBuckets,
		}),
		OutboxPublishFailuresTotal: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "outbox_publish_failures_total",
			Help: "Total de falhas ao publicar eventos pendentes da outbox.",
		}),
		MessageProcessingSeconds: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "message_processing_seconds",
			Help:    "Latência de processamento de uma operação de aposta, por canal de entrada.",
			Buckets: prometheus.DefBuckets,
		}, []string{"channel"}),
		InboxDuplicatesTotal: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "sqs_inbox_duplicates_total",
			Help: "Total de mensagens SQS identificadas como duplicadas pela inbox.",
		}),
		MessageDeliveryFailuresTotal: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "sqs_message_delivery_failures_total",
			Help: "Total de falhas ao processar mensagens SQS, candidatas a redrive/DLQ.",
		}),
		ReconciliationChecksTotal: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "wallet_reconciliation_checks_total",
			Help: "Total de reconciliações de saldo executadas.",
		}),
		ReconciliationDivergentTotal: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "wallet_reconciliation_divergences_total",
			Help: "Total de reconciliações que encontraram divergência entre saldo armazenado e calculado.",
		}),
		registry: registry,
	}

	registry.MustRegister(
		m.WagerTransactionsTotal,
		m.IdempotentReplaysTotal,
		m.OptimisticLockRetriesTotal,
		m.TooManyRetriesTotal,
		m.OutboxPublishLatencySeconds,
		m.OutboxPublishFailuresTotal,
		m.MessageProcessingSeconds,
		m.InboxDuplicatesTotal,
		m.MessageDeliveryFailuresTotal,
		m.ReconciliationChecksTotal,
		m.ReconciliationDivergentTotal,
	)

	return m
}

func (m *Metrics) Registry() *prometheus.Registry {
	return m.registry
}

func (m *Metrics) ObserveMessageProcessing(channel string, start time.Time) {
	m.MessageProcessingSeconds.WithLabelValues(channel).Observe(time.Since(start).Seconds())
}
