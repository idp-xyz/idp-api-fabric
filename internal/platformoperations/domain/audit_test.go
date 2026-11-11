package domain

import (
	"errors"
	"testing"
	"time"
)

func completeRecord() AuditRecord {
	return AuditRecord{
		ID:            "6f1d2c3a-0b7e-4c59-9a43-5e2f8d1c7b60",
		RecordedAt:    time.Date(2026, 11, 11, 9, 0, 0, 0, time.UTC),
		Actor:         Actor{Issuer: "https://issuer.example", Subject: "alice"},
		Action:        "product.register",
		Outcome:       OutcomeSucceeded,
		Rule:          "AC-08",
		ProductID:     "1c9a4e7b-52d0-4f8e-b6a1-90c3d2e4f5a6",
		ResourceType:  "Product",
		ResourceID:    "1c9a4e7b-52d0-4f8e-b6a1-90c3d2e4f5a6",
		CorrelationID: "3b0f6a2d-8c41-4e7f-a5d9-0e1c2b3a4d5e",
	}
}

func TestACompleteRecordIsValid(t *testing.T) {
	if err := completeRecord().Validate(); err != nil {
		t.Fatalf("a complete record was refused: %v", err)
	}

	// A refusal in the platform scope concerns no product and leaves no
	// resource behind.
	refusal := completeRecord()
	refusal.Outcome, refusal.Rule, refusal.ProductID, refusal.ResourceID = OutcomeDenied, "DENY-03", "", ""
	if err := refusal.Validate(); err != nil {
		t.Fatalf("a refusal without a product or a resource was refused: %v", err)
	}
}

func TestARecordThatSaysTooLittleIsIncomplete(t *testing.T) {
	cases := map[string]func(record *AuditRecord){
		"no identifier":                func(r *AuditRecord) { r.ID = "" },
		"no time":                      func(r *AuditRecord) { r.RecordedAt = time.Time{} },
		"no issuer of the actor":       func(r *AuditRecord) { r.Actor.Issuer = "" },
		"no subject of the actor":      func(r *AuditRecord) { r.Actor.Subject = "" },
		"no action":                    func(r *AuditRecord) { r.Action = "" },
		"no outcome":                   func(r *AuditRecord) { r.Outcome = "" },
		"an outcome nobody defined":    func(r *AuditRecord) { r.Outcome = "MAYBE" },
		"no rule":                      func(r *AuditRecord) { r.Rule = "" },
		"no kind of resource":          func(r *AuditRecord) { r.ResourceType = "" },
		"a success without a resource": func(r *AuditRecord) { r.ResourceID = "" },
		"no correlation identifier":    func(r *AuditRecord) { r.CorrelationID = "" },
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			record := completeRecord()
			change(&record)
			if err := record.Validate(); !errors.Is(err, ErrAuditRecordIncomplete) {
				t.Fatalf("got %v, want %v", err, ErrAuditRecordIncomplete)
			}
		})
	}
}
