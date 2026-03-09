package queue

import "context"

// RipSpecEncoder encodes a rip spec envelope to JSON text.
// Satisfied by ripspec.Envelope without importing that package.
type RipSpecEncoder interface {
	Encode() (string, error)
}

// PersistRipSpec encodes the rip spec and writes it to the item's rip_spec_data
// column via store.Update.
func PersistRipSpec(ctx context.Context, store *Store, item *Item, encoder RipSpecEncoder) error {
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
	return store.Update(item)
}
