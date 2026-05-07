package queue

import (
	"context"
	"errors"
)

// RipSpecEncoder encodes a rip spec envelope to JSON text.
// Satisfied by ripspec.Envelope without importing that package.
type RipSpecEncoder interface {
	Encode() (string, error)
}

// PersistRipSpec encodes the rip spec and writes it to the item's rip_spec_data
// column plus related work-state fields. Lifecycle fields are intentionally not
// persisted here.
func PersistRipSpec(ctx context.Context, store *Store, item *Item, encoder RipSpecEncoder) error {
	if store == nil {
		return errors.New("persist rip spec: nil queue store")
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	data, err := encoder.Encode()
	if err != nil {
		return err
	}
	item.RipSpecData = data
	return store.UpdateWorkState(item)
}
