package openapi_test

import (
	"slices"
	"strings"
	"testing"

	"go.idp.xyz/idp-api-fabric/internal/policyreview/adapters/openapi"
	"go.idp.xyz/idp-api-fabric/internal/policyreview/domain"
)

const eventSpec = `asyncapi: '3.1.0'
info: {title: Shipments, version: '1'}
servers:
  broker:
    host: broker.example.test:9092
    protocol: kafka
    security: [{type: userPassword}]
channels:
  arrivals:
    address: shipment.arrived
    messages:
      shipmentArrived:
        payload: {type: string}
operations:
  observe:
    action: receive
    channel: {$ref: '#/channels/arrivals'}
`

func eventSource(content string) domain.Source {
	return domain.Source{
		Specification: domain.AsyncAPI, Entrypoint: "asyncapi.yaml",
		Files: []domain.SourceFile{file("asyncapi.yaml", content)}, Closure: domain.ClosureVerified,
	}
}

func TestAsyncAPIUsesTheEmbeddedSchemaAndChecksServerSecurity(t *testing.T) {
	for _, item := range []struct {
		name, source, failedRule string
	}{
		{"valid", eventSpec, ""},
		{"invalid action", strings.ReplaceAll(eventSpec, "action: receive", "action: publish"), "F04-GOV-011"},
		{"missing security", strings.ReplaceAll(eventSpec, "    security: [{type: userPassword}]\n", ""), "F04-GOV-006"},
	} {
		t.Run(item.name, func(t *testing.T) {
			observations := (openapi.EventEvaluator{}).Evaluate(eventSource(item.source), domain.Baseline().Rules())
			var seenSchema, seenFailure bool
			for _, observation := range observations {
				seenSchema = seenSchema || observation.RuleID == "F04-GOV-011"
				seenFailure = seenFailure || observation.RuleID == item.failedRule && len(observation.Findings) > 0
				if observation.Undecided || (len(observation.Findings) > 0) != (observation.RuleID == item.failedRule) {
					t.Errorf("unexpected observation %+v", observation)
				}
			}
			if !seenSchema {
				t.Fatal("the embedded schema was not selected")
			}
			if item.failedRule != "" && !seenFailure {
				t.Fatalf("%s did not report a finding", item.failedRule)
			}
		})
	}
}

func TestAsyncAPICompatibilityClassifiesChangesConservatively(t *testing.T) {
	for _, item := range []struct {
		name, candidate string
		want            domain.Compatibility
		change          string
	}{
		{"same document", eventSpec, domain.Compatible, ""},
		{"documentation", strings.ReplaceAll(eventSpec, "title: Shipments", "title: Shipment Events"), domain.Compatible, ""},
		{"removed operation", strings.Split(eventSpec, "operations:")[0], domain.Breaking, "OPERATION_REMOVED"},
		{"changed direction", strings.ReplaceAll(eventSpec, "action: receive", "action: send"), domain.Breaking, "OPERATION_DIRECTION_CHANGED"},
		{"changed address", strings.ReplaceAll(eventSpec, "shipment.arrived", "shipment.delivered"), domain.Breaking, "CHANNEL_ADDRESS_CHANGED"},
		{"changed payload", strings.ReplaceAll(eventSpec, "payload: {type: string}", "payload: {type: number}"), domain.Unknown, "CHANNEL_CHANGED"},
		{"invalid action", strings.ReplaceAll(eventSpec, "action: receive", "action: publish"), domain.Unknown, "DOCUMENT_UNSUPPORTED"},
	} {
		t.Run(item.name, func(t *testing.T) {
			got := (openapi.EventComparator{}).Compare(eventSource(eventSpec), eventSource(item.candidate))
			if got.Classification != item.want {
				t.Fatalf("got %+v, want %s", got, item.want)
			}
			if item.change == "" && len(got.Changes) != 0 {
				t.Fatalf("got unexpected changes %+v", got.Changes)
			}
			if item.change != "" && !slices.ContainsFunc(got.Changes, func(change domain.CompatibilityChange) bool {
				return change.Code == item.change
			}) {
				t.Errorf("got %+v, want %s", got.Changes, item.change)
			}
		})
	}
}
