package bentocontract_test

import (
	"testing"
	"time"

	"go.idp.xyz/idp-bento-go/eventing"
	bentopostgres "go.idp.xyz/idp-bento-go/postgres"
	"go.idp.xyz/idp-bento-go/postgres/inbox"
	"go.idp.xyz/idp-bento-go/postgres/outbox"
	"go.idp.xyz/idp-bento-go/testkit"

	"go.idp.xyz/idp-api-fabric/internal/platform/db"
	"go.idp.xyz/idp-api-fabric/internal/platform/db/pgtest"
)

var contractTime = time.Date(2026, 11, 11, 3, 0, 0, 0, time.UTC)

// frameworkDB opens a database of its own the way a Fabric process does:
// migrated by the Fabric plan, then checked and bound by db.Open as the
// application role. The contracts therefore also prove that the privileges
// Fabric grants that role are enough for the framework.
func frameworkDB(t *testing.T) *bentopostgres.DB {
	t.Helper()

	database, err := db.Open(t.Context(), pgtest.MigratedDatabase(t).Application)
	if err != nil {
		t.Fatalf("open the database: %v", err)
	}
	t.Cleanup(database.Close)
	return database.Framework
}

// envelope is the framework's own valid envelope with the identity, partition
// and times a scenario needs. recordedAfter orders the enqueue sequence inside
// a partition.
func envelope(id eventing.EventID, partitionKey string, recordedAfter time.Duration) eventing.Envelope {
	e := testkit.ValidEnvelope()
	e.ID = id
	e.PartitionKey = partitionKey
	e.RecordedAt = contractTime.Add(recordedAfter)
	e.OccurredAt = e.RecordedAt.Add(-time.Second)
	return e
}

// The framework's Outbox contract holds on the schema Fabric migrates: the
// order inside a partition, the lease on a claim and every way a delivery is
// finalized are proven by the framework's runner on real SQL.
func TestOutboxContractHoldsOnTheFabricSchema(t *testing.T) {
	testkit.RunOutboxContract(t, func(t *testing.T) testkit.OutboxContractScenario {
		framework := frameworkDB(t)
		store, err := outbox.NewStore(framework)
		if err != nil {
			t.Fatalf("build the outbox store: %v", err)
		}
		return testkit.OutboxContractScenario{
			Transactor:     framework.Transactor(),
			Writer:         store,
			Claimer:        store,
			Finalizer:      store,
			First:          envelope("fabric-contract-0001", "scope/first", 0),
			SamePartition:  envelope("fabric-contract-0002", "scope/first", time.Second),
			OtherPartition: envelope("fabric-contract-0003", "scope/other", 2*time.Second),
			Now:            contractTime.Add(time.Minute),
			LeaseFor:       30 * time.Second,
		}
	})
}

// The framework's Inbox contract holds on the schema Fabric migrates:
// consuming an event twice has one effect, and a row still PROCESSING cannot
// be committed.
func TestInboxContractHoldsOnTheFabricSchema(t *testing.T) {
	testkit.RunInboxContract(t, func(t *testing.T) testkit.InboxContractScenario {
		framework := frameworkDB(t)
		store, err := inbox.NewStore(framework)
		if err != nil {
			t.Fatalf("build the inbox store: %v", err)
		}
		return testkit.InboxContractScenario{
			Transactor: framework.Transactor(),
			Starter:    store,
			Completer:  store,
			Key: eventing.InboxKey{
				Consumer: "fabric-contract-consumer",
				Source:   testkit.ValidEnvelope().Source,
				EventID:  "fabric-contract-0001",
			},
			Now: contractTime,
		}
	})
}
