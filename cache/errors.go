package cache

import "errors"

var (
	// ErrInvalidConfig indicates an invalid configuration.
	ErrInvalidConfig = errors.New("invalid config")
	// ErrKeyNotFound indicates no value exists for the given key.
	//
	// This error is returned by Get operations when the requested key has never been
	// set in the cache or has been explicitly deleted. It is also returned by
	// GetAndDelete when the key does not exist.
	//
	// Example:
	//	_, err := cache.Get(ctx, "nonexistent-key")
	//	if errors.Is(err, cache.ErrKeyNotFound) {
	//	    // Handle missing key
	//	}
	ErrKeyNotFound = errors.New("key not found")

	// ErrKeyExists indicates a conflicting set when the key already exists.
	//
	// This error is returned by SetOrFail operations when attempting to set a value
	// for a key that already exists in the cache and has not expired. This is useful
	// for implementing atomic "create if not exists" operations and preventing
	// race conditions in concurrent scenarios.
	//
	// Example:
	//	// Try to set a value only if key doesn't exist
	//	err := cache.SetOrFail(ctx, "lock-key", []byte("locked"))
	//	if errors.Is(err, cache.ErrKeyExists) {
	//	    // Key already exists, handle conflict
	//	}
	ErrKeyExists = errors.New("key already exists")

	// ErrNotAnInteger indicates a stored value that cannot be used as a counter.
	//
	// This error is returned by Increment when the value already stored under the
	// key is not the decimal representation of an int64 (e.g. "abc", "007", "+7"),
	// which happens when Set overwrote a counter with arbitrary bytes.
	//
	// Example:
	//	_, err := cache.Increment(ctx, "counter", 1)
	//	if errors.Is(err, cache.ErrNotAnInteger) {
	//	    // Stored value is not a counter; overwrite or delete the key
	//	}
	ErrNotAnInteger = errors.New("value is not an integer")

	// ErrCounterOverflow indicates an increment that exceeds the int64 range.
	//
	// This error is returned by Increment when adding delta to the stored value
	// would overflow or underflow int64. The counter is left unchanged.
	//
	// Example:
	//	_, err := cache.Increment(ctx, "counter", 1)
	//	if errors.Is(err, cache.ErrCounterOverflow) {
	//	    // Counter is at math.MaxInt64
	//	}
	ErrCounterOverflow = errors.New("counter overflow")

	ErrFailedToCreateZeroValue = errors.New("failed to create zero item")
)
