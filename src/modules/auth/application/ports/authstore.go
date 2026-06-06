package ports

import (
	"context"
	"errors"

	"github.com/Mapex-Solutions/mapexMQTTBroket/src/modules/auth/domain/entities"
)

// AuthStore is the read-side of the broker plugin's auth path. The
// MOSQ_EVT_BASIC_AUTH callback hits Get on every CONNECT, so the
// implementation MUST keep p99 latency well below the bcrypt cost (~50ms) —
// otherwise the broker thread serializes CONNECTs and a reconnect storm queues
// at the TCP layer.
type AuthStore interface {
	// Get returns the AuthEntry for an asset, keyed only by assetUUID —
	// globally unique. Tenant scoping flows from the entry's OrgId field, not
	// from the wire username. Returns ErrAuthEntryNotFound when the asset does
	// not exist and ErrAuthStoreUnavailable when every layer was unreachable.
	Get(ctx context.Context, assetUUID string) (*entities.AuthEntry, error)

	// Invalidate removes the cached entry for an asset. Idempotent; a missing
	// entry is not an error. Driven by the fanout consumer on asset CRUD.
	Invalidate(ctx context.Context, assetUUID string) error

	// Close releases the store's resources. Called at plugin cleanup; safe to
	// call multiple times.
	Close() error
}

// AuthLookup is the last-resort read used as the tiered store's L3 fallback: a
// direct fetch of an asset's AuthEntry from the assets service. Kept as a port
// so the cache layer depends on this contract, not on the concrete HTTP
// client.
type AuthLookup interface {
	LookupEntry(ctx context.Context, assetUUID string) (*entities.AuthEntry, error)
}

var (
	// ErrAuthEntryNotFound — the asset doesn't exist in any layer.
	ErrAuthEntryNotFound = errors.New("auth entry not found")

	// ErrAuthStoreUnavailable — every cache layer is unreachable. Caller MUST
	// treat as fail-closed: deny the CONNECT rather than risk admitting an
	// unverified device.
	ErrAuthStoreUnavailable = errors.New("auth store unavailable")
)
