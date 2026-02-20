package queue

import "context"

// RipSpecEncoder can serialize itself for storage. This is satisfied by
// *ripspec.Envelope without requiring a direct import of that package.
type RipSpecEncoder interface {
	Encode() (string, error)
}

// PersistRipSpec encodes env and writes the result to item via store.Update.
// On success the updated item fields (including any store-generated values)
// are written back through the item pointer. Returns a non-nil error when
// encoding or persistence fails; callers decide how to log the result.
func PersistRipSpec(ctx context.Context, store *Store, item *Item, env RipSpecEncoder) error {
	encoded, err := env.Encode()
	if err != nil {
		return err
	}
	copy := *item
	copy.RipSpecData = encoded
	if store != nil {
		if err := store.Update(ctx, &copy); err != nil {
			return err
		}
	}
	*item = copy
	return nil
}
