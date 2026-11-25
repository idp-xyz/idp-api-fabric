package workers

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"go.idp.xyz/idp-bento-go/eventing"
)

const (
	outboxPollInterval   = time.Second
	outboxDatabaseBudget = 5 * time.Second
	outboxPublishBudget  = 10 * time.Second
	outboxLeaseDuration  = 30 * time.Second
	outboxAttemptLimit   = 12
	outboxMaxRetryDelay  = 5 * time.Minute

	outboxPublishRejected  eventing.FailureCode = "outbox.receiver_rejected"
	outboxPublishUncertain eventing.FailureCode = "outbox.delivery_uncertain"
)

type outboxStore interface {
	eventing.OutboxClaimer
	eventing.OutboxFinalizer
}

// RunOutbox relays committed events until the process is stopped. A lease
// fences updates to the queue. Receivers deduplicate delivery by source and
// event ID because a worker can lose its lease after the receiver accepts it.
func RunOutbox(ctx context.Context, store outboxStore, publisher eventing.Publisher, logger *slog.Logger) {
	for ctx.Err() == nil {
		handled, err := deliverOutboxEvent(ctx, store, publisher, logger)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			logger.WarnContext(ctx, "outbox store operation failed", "error", err)
		}
		if handled && err == nil {
			continue
		}
		select {
		case <-time.After(outboxPollInterval):
		case <-ctx.Done():
			return
		}
	}
}

func deliverOutboxEvent(ctx context.Context, store outboxStore, publisher eventing.Publisher, logger *slog.Logger) (bool, error) {
	claimCtx, cancelClaim := context.WithTimeout(ctx, outboxDatabaseBudget)
	deliveries, err := store.Claim(claimCtx, eventing.OutboxClaim{
		Now: time.Now(), Limit: 1, LeaseFor: outboxLeaseDuration, MaxAttempts: outboxAttemptLimit,
	})
	cancelClaim()
	if err != nil || len(deliveries) == 0 {
		return false, err
	}
	delivery := deliveries[0]
	publishCtx, cancelPublish := context.WithTimeout(ctx, outboxPublishBudget)
	publishErr := publisher.Publish(publishCtx, delivery.Envelope)
	cancelPublish()
	if ctx.Err() != nil {
		return true, ctx.Err()
	}

	finishCtx, cancelFinish := context.WithTimeout(ctx, outboxDatabaseBudget)
	defer cancelFinish()
	now := time.Now()
	switch {
	case publishErr == nil:
		return true, store.MarkPublished(finishCtx, delivery.Ref, now)
	case errors.Is(publishErr, eventing.ErrInvalidEnvelope):
		logger.WarnContext(ctx, "outbox event has an invalid envelope",
			"source", delivery.Ref.Source, "event_id", delivery.Ref.EventID)
		return true, store.Abandon(finishCtx, delivery.Ref, now, eventing.FailureEnvelopeInvalidOnPublish)
	default:
		code := outboxPublishUncertain
		if errors.Is(publishErr, eventing.ErrPublishRejected) {
			code = outboxPublishRejected
		}
		logger.WarnContext(ctx, "outbox event was unacknowledged", "source", delivery.Ref.Source,
			"event_id", delivery.Ref.EventID, "attempt", delivery.Attempt, "code", code)
		return true, store.RecordFailure(finishCtx, delivery.Ref, eventing.DeliveryFailure{
			FailedAt: now, RetryAt: now.Add(outboxRetryDelay(delivery.Failures)), Code: code, MaxFailures: outboxAttemptLimit,
		})
	}
}

func outboxRetryDelay(failures uint32) time.Duration {
	delay := time.Second << min(failures, 9)
	return min(delay, outboxMaxRetryDelay)
}
