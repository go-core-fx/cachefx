package cache

import (
	"container/list"
	"context"
	"math"
	"strconv"
	"sync"
	"time"
)

// MemoryCache implements an in-memory cache with TTL support.
//
// This implementation stores all data in a Go map protected by a read-write mutex,
// making it safe for concurrent access by multiple goroutines. Items are automatically
// checked for expiration on access, and an expired item is removed by the Get that
// observes it. Entries that are never read again stay in memory until an explicit
// Cleanup operation or an overwrite.
//
// Constructed with [WithMaxEntries], the cache also caps how many entries it
// holds: inserting a new key while at the cap evicts the least recently used
// entry. See [WithMaxEntries] for the full recency rules. An unbounded cache
// keeps no recency order and serves reads under the read lock; a capped cache
// takes the write lock on a Get hit to refresh recency.
//
// The memory cache is suitable for:
//   - Single-process applications
//   - Caching small to medium amounts of data
//   - Scenarios where low latency is critical
//   - Temporary caching that doesn't need persistence
//
// For distributed caching or persistence, consider using the Redis implementation instead.
type MemoryCache struct {
	items map[string]*memoryItem
	ttl   time.Duration

	// maxEntries caps the number of entries when positive. The cache is
	// unbounded when it is zero or less.
	maxEntries int

	// elems indexes order by key, and order lists keys from most to least
	// recently used. Both are nil when the cache is unbounded; otherwise
	// every key in items has an element in order and elems.
	elems map[string]*list.Element
	order *list.List

	mux sync.RWMutex
}

// NewMemory creates a new in-memory cache with the specified default TTL
// and optional constructor options such as [WithMaxEntries].
//
// The TTL parameter sets the default time-to-live for items stored in the cache.
// A TTL of zero means items do not expire by default, but individual items
// can still have their own TTL set via options.
//
// Parameters:
//   - ttl: The default TTL for cache items. Zero means no expiration.
//   - opts: Optional constructor options (e.g., a maximum entry count)
//
// Returns:
//   - *MemoryCache: A new in-memory cache instance
//
// Example:
//
//	// Create a cache with 1 hour default TTL
//	cache := cache.NewMemory(time.Hour)
//
//	// Create a cache with no expiration
//	cache := cache.NewMemory(0)
//
//	// Create a cache that holds at most 1000 entries
//	cache := cache.NewMemory(time.Hour, cache.WithMaxEntries(1000))
func NewMemory(ttl time.Duration, opts ...MemoryOption) *MemoryCache {
	cfg := new(memoryConfig)
	for _, opt := range opts {
		opt(cfg)
	}

	m := &MemoryCache{
		items:      make(map[string]*memoryItem),
		ttl:        ttl,
		maxEntries: cfg.maxEntries,
		elems:      nil,
		order:      nil,
		mux:        sync.RWMutex{},
	}

	if m.maxEntries > 0 {
		m.elems = make(map[string]*list.Element)
		m.order = list.New()
	}

	return m
}

// memoryItem represents a single item in the memory cache.
type memoryItem struct {
	value      []byte
	validUntil time.Time
}

// newMemoryItem creates a new memory item with the specified value and options.
func newMemoryItem(value []byte, opts options) *memoryItem {
	item := &memoryItem{
		value:      value,
		validUntil: opts.validUntil,
	}

	return item
}

// isExpired checks if the item has expired at the given time.
//
// An item is considered expired if:
//   - The item is nil (safety check)
//   - The item has a non-zero validUntil time and the current time is after validUntil
//
// Parameters:
//   - now: The time to check expiration against
//
// Returns:
//   - bool: True if the item has expired, false otherwise
func (i *memoryItem) isExpired(now time.Time) bool {
	if i == nil {
		return true
	}

	return !i.validUntil.IsZero() && now.After(i.validUntil)
}

// trackUse marks key as the most recently used entry.
//
// It is a no-op when the cache is unbounded or the key is not tracked.
// The caller must hold the write lock.
func (m *MemoryCache) trackUse(key string) {
	if m.order == nil {
		return
	}

	if elem, ok := m.elems[key]; ok {
		m.order.MoveToFront(elem)
	}
}

// trackInsert registers key as the most recently used entry, tracking it if
// it is not tracked yet and moving it to the front if it is.
//
// It is a no-op when the cache is unbounded. The caller must hold the write
// lock.
func (m *MemoryCache) trackInsert(key string) {
	if m.order == nil {
		return
	}

	if elem, ok := m.elems[key]; ok {
		m.order.MoveToFront(elem)

		return
	}

	m.elems[key] = m.order.PushFront(key)
}

// trackRemove drops key from the recency order.
//
// It is a no-op when the cache is unbounded or the key is not tracked. The
// caller must hold the write lock.
func (m *MemoryCache) trackRemove(key string) {
	if m.order == nil {
		return
	}

	if elem, ok := m.elems[key]; ok {
		m.order.Remove(elem)
		delete(m.elems, key)
	}
}

// trackReset empties the recency order.
//
// It is a no-op when the cache is unbounded. The caller must hold the write
// lock.
func (m *MemoryCache) trackReset() {
	if m.order == nil {
		return
	}

	m.order.Init()
	clear(m.elems)
}

// evictLocked evicts the least recently used entries until the cache fits
// its maximum entry count.
//
// It is a no-op when the cache is unbounded. The caller must hold the write
// lock.
func (m *MemoryCache) evictLocked() {
	if m.order == nil {
		return
	}

	for len(m.items) > m.maxEntries {
		back := m.order.Back()
		if back == nil {
			break
		}

		if key, ok := back.Value.(string); ok {
			delete(m.items, key)
			delete(m.elems, key)
		}

		m.order.Remove(back)
	}
}

// Cleanup removes all expired items from the memory cache.
//
// This method scans through all items in the cache and removes any that have expired.
// The operation is performed atomically with a write lock to ensure consistency.
// Note that this is a manual cleanup operation - expired items are also checked
// during normal Get operations, but this method explicitly removes them from memory.
//
// Parameters:
//   - ctx: Context for cancellation and timeouts (currently unused but kept for interface compatibility)
//
// Returns:
//   - error: Always nil for memory cache
//
// Example:
//
//	// Periodically clean up expired items
//	err := cache.Cleanup(ctx)
func (m *MemoryCache) Cleanup(_ context.Context) error {
	m.cleanup(func() {})

	return nil
}

// Delete removes the item associated with the given key from the memory cache.
//
// If the key does not exist, this operation performs no action and returns nil.
// The operation is safe for concurrent use by multiple goroutines.
//
// Parameters:
//   - ctx: Context for cancellation and timeouts (currently unused but kept for interface compatibility)
//   - key: The key to remove from the cache
//
// Returns:
//   - error: Always nil for memory cache
//
// Example:
//
//	// Remove a specific key
//	err := cache.Delete(ctx, "user:123")
func (m *MemoryCache) Delete(_ context.Context, key string) error {
	m.mux.Lock()
	delete(m.items, key)
	m.trackRemove(key)
	m.mux.Unlock()

	return nil
}

// Drain returns a map of all non-expired items in the memory cache and clears the cache.
//
// The returned map is a snapshot of the cache at the time of the call.
// After this operation, the cache will be empty. This is useful for cache migration,
// backup, or when shutting down an application. The operation is performed atomically
// with a write lock to ensure consistency.
//
// Parameters:
//   - ctx: Context for cancellation and timeouts (currently unused but kept for interface compatibility)
//
// Returns:
//   - map[string][]byte: A map containing all non-expired key-value pairs
//   - error: Always nil for memory cache
//
// Example:
//
//	// Get all items and clear the cache
//	items, err := cache.Drain(ctx)
//	for key, value := range items {
//	    log.Printf("Drained: %s = %s", key, string(value))
//	}
func (m *MemoryCache) Drain(_ context.Context) (map[string][]byte, error) {
	var cpy map[string]*memoryItem

	m.cleanup(func() {
		cpy = m.items
		m.items = make(map[string]*memoryItem)
		m.trackReset()
	})

	items := make(map[string][]byte, len(cpy))
	for key, item := range cpy {
		items[key] = item.value
	}

	return items, nil
}

// Get retrieves the value for the given key from the memory cache.
//
// The behavior depends on the key's existence and expiration state:
//   - If the key exists and has not expired, returns the value and nil error
//   - If the key does not exist or has expired, returns nil and ErrKeyNotFound
//
// On an existing key, Get marks the key as the most recently used when the
// cache has a maximum entry count (see [WithMaxEntries]); an unbounded cache
// performs no recency bookkeeping.
//
// GetOptions can be used to modify the behavior, such as updating TTL,
// deleting the key after retrieval, or setting a new expiration time.
//
// Parameters:
//   - ctx: Context for cancellation and timeouts (currently unused but kept for interface compatibility)
//   - key: The key to retrieve
//   - opts: Optional operations to perform during retrieval (e.g., AndDelete, AndSetTTL)
//
// Returns:
//   - []byte: The cached value if found and not expired
//   - error: nil on success, ErrKeyNotFound if key doesn't exist or has expired,
//     otherwise an error
//
// Example:
//
//	// Simple get
//	value, err := cache.Get(ctx, "user:123")
//
//	// Get and extend TTL by 30 minutes
//	value, err := cache.Get(ctx, "session:abc", cache.AndUpdateTTL(30*time.Minute))
//
//	// Get and delete atomically
//	value, err := cache.Get(ctx, "temp:xyz", cache.AndDelete())
func (m *MemoryCache) Get(_ context.Context, key string, opts ...GetOption) ([]byte, error) {
	// The getter releases its lock before returning so that getItem can take
	// a write lock when it needs to remove an expired entry.
	switch {
	case len(opts) == 0 && m.order == nil:
		return m.getValue(key, func() (*memoryItem, bool) {
			m.mux.RLock()
			item, ok := m.items[key]
			m.mux.RUnlock()

			return item, ok
		})
	case len(opts) == 0:
		// A capped cache refreshes recency on a hit, which takes the write lock.
		return m.getValue(key, func() (*memoryItem, bool) {
			m.mux.Lock()
			defer m.mux.Unlock()

			now := time.Now()
			item, ok := m.items[key]
			if ok && !item.isExpired(now) {
				m.trackUse(key)
			}

			return item, ok
		})
	default:
		return m.getValue(key, m.optionsGetter(key, opts))
	}
}

// optionsGetter returns a getter that applies the given GetOptions under the
// write lock, refreshing the key's recency before rescheduling or deleting
// the entry.
//
// The getter releases its lock before returning so that getItem can take
// a write lock when it needs to remove an expired entry.
func (m *MemoryCache) optionsGetter(key string, opts []GetOption) func() (*memoryItem, bool) {
	return func() (*memoryItem, bool) {
		o := new(getOptions)
		o.apply(opts...)

		m.mux.Lock()
		defer m.mux.Unlock()

		now := time.Now()
		item, ok := m.items[key]

		if !ok || item.isExpired(now) {
			return item, ok
		}

		if o.delete {
			delete(m.items, key)
			m.trackRemove(key)

			return item, ok
		}

		m.trackUse(key)

		switch {
		case o.validUntil != nil:
			item.validUntil = *o.validUntil
		case o.setTTL != nil:
			item.validUntil = now.Add(*o.setTTL)
		case o.updateTTL != nil:
			if item.validUntil.IsZero() {
				item.validUntil = now.Add(*o.updateTTL)
			} else {
				item.validUntil = item.validUntil.Add(*o.updateTTL)
			}
		case o.defaultTTL:
			if m.ttl > 0 {
				item.validUntil = now.Add(m.ttl)
			} else {
				item.validUntil = time.Time{}
			}
		}

		return item, ok
	}
}

// GetAndDelete retrieves the value for the given key and atomically deletes it from the memory cache.
//
// This is equivalent to calling Get with the AndDelete option, but provides a more
// convenient API for the common pattern of reading and removing a value in one operation.
//
// Parameters:
//   - ctx: Context for cancellation and timeouts
//   - key: The key to retrieve and delete
//
// Returns:
//   - []byte: The cached value if found and not expired
//   - error: nil on success, ErrKeyNotFound if key doesn't exist or has expired,
//     otherwise an error
//
// Example:
//
//	// Atomically get and remove a value
//	value, err := cache.GetAndDelete(ctx, "queue:item:123")
func (m *MemoryCache) GetAndDelete(ctx context.Context, key string) ([]byte, error) {
	return m.Get(ctx, key, AndDelete())
}

// Increment atomically adds delta to the integer counter stored under the given key
// and returns the new value. A negative delta decrements the counter.
//
// A missing or expired key is created holding delta, applying the cache's default TTL
// unless overridden by options. An existing key keeps its value's TTL and the options
// are ignored. The stored value must be the decimal representation of an int64
// (strconv.FormatInt); anything else returns ErrNotAnInteger. The counter is left
// unchanged when the result would leave the int64 range.
//
// On a capped cache (see [WithMaxEntries]) Increment marks the key as the most
// recently used, both when it updates an existing counter and when it creates
// one; a created counter counts toward the cap and may evict the least
// recently used entry.
//
// The operation is performed atomically with a write lock, so concurrent Increment
// calls never lose updates.
//
// Parameters:
//   - ctx: Context for cancellation and timeouts (currently unused but kept for interface compatibility)
//   - key: The key of the counter to increment
//   - delta: The signed value to add to the counter
//   - opts: Optional expiration applied only when the counter is created (e.g., custom TTL)
//
// Returns:
//   - int64: The new counter value
//   - error: nil on success, ErrNotAnInteger if the stored value is not an int64
//     decimal, ErrCounterOverflow if the result would leave the int64 range,
//     otherwise an error
//
// Example:
//
//	// Count page views; the first call creates the counter with 1
//	n, err := cache.Increment(ctx, "views:home", 1)
func (m *MemoryCache) Increment(_ context.Context, key string, delta int64, opts ...Option) (int64, error) {
	m.mux.Lock()
	defer m.mux.Unlock()

	item, ok := m.items[key]
	if !ok || item.isExpired(time.Now()) {
		m.items[key] = m.newItem([]byte(strconv.FormatInt(delta, 10)), opts...)
		m.trackInsert(key)
		m.evictLocked()

		return delta, nil
	}

	m.trackUse(key)

	cur, err := strconv.ParseInt(string(item.value), 10, 64)
	if err != nil || strconv.FormatInt(cur, 10) != string(item.value) {
		return 0, ErrNotAnInteger
	}

	next, ok := addInt64(cur, delta)
	if !ok {
		return 0, ErrCounterOverflow
	}

	item.value = []byte(strconv.FormatInt(next, 10))

	return next, nil
}

// Set stores the value for the given key in the memory cache, overwriting any existing value.
//
// The value will be stored with the default TTL configured for the cache,
// unless overridden by options. If the key already exists, its value and TTL will be updated.
//
// Set never changes the recency of a key already present: on a capped cache
// (see [WithMaxEntries]) the entry keeps its position in the eviction order,
// while a key that is not present is inserted as the most recently used.
//
// Parameters:
//   - ctx: Context for cancellation and timeouts (currently unused but kept for interface compatibility)
//   - key: The key to store the value under
//   - value: The value to store as a byte slice
//   - opts: Optional configuration for this specific item (e.g., custom TTL)
//
// Returns:
//   - error: Always nil for memory cache
//
// Example:
//
//	// Set with default TTL
//	err := cache.Set(ctx, "user:123", []byte("user data"))
//
//	// Set with custom TTL
//	err := cache.Set(ctx, "session:abc", []byte("session data"), cache.WithTTL(30*time.Minute))
func (m *MemoryCache) Set(_ context.Context, key string, value []byte, opts ...Option) error {
	m.mux.Lock()
	_, exists := m.items[key]
	m.items[key] = m.newItem(value, opts...)
	if !exists {
		m.trackInsert(key)
	}
	m.evictLocked()
	m.mux.Unlock()

	return nil
}

// SetOrFail stores the value for the given key only if the key does not already exist.
//
// This is an atomic operation that prevents race conditions when multiple goroutines
// might try to set the same key simultaneously. If the key exists but has expired,
// it will be overwritten.
//
// Recency follows Set: a key already present (even an expired one) keeps its
// position in a capped cache's eviction order, while a key that is not
// present is inserted as the most recently used (see [WithMaxEntries]).
//
// Parameters:
//   - ctx: Context for cancellation and timeouts (currently unused but kept for interface compatibility)
//   - key: The key to store the value under
//   - value: The value to store as a byte slice
//   - opts: Optional configuration for this specific item (e.g., custom TTL)
//
// Returns:
//   - error: nil on success, ErrKeyExists if the key already exists and is not expired,
//     otherwise an error
//
// Example:
//
//	// Try to set a value only if key doesn't exist
//	err := cache.SetOrFail(ctx, "lock:resource", []byte("locked"))
//	if errors.Is(err, cache.ErrKeyExists) {
//	    // Key already exists, handle conflict
//	}
func (m *MemoryCache) SetOrFail(_ context.Context, key string, value []byte, opts ...Option) error {
	m.mux.Lock()
	defer m.mux.Unlock()

	item, exists := m.items[key]
	if exists && !item.isExpired(time.Now()) {
		return ErrKeyExists
	}

	m.items[key] = m.newItem(value, opts...)
	if !exists {
		m.trackInsert(key)
	}
	m.evictLocked()

	return nil
}

// newItem creates a new memory item with the specified value and options.
//
// This method applies the cache's default TTL if no explicit expiration is set
// in the options, and then creates a new memoryItem with the combined configuration.
//
// Parameters:
//   - value: The value to store in the item
//   - opts: Optional configuration for the item (e.g., custom TTL)
//
// Returns:
//   - *memoryItem: A new memory item with the specified configuration
func (m *MemoryCache) newItem(value []byte, opts ...Option) *memoryItem {
	o := options{
		validUntil: time.Time{},
	}
	if m.ttl > 0 {
		o.validUntil = time.Now().Add(m.ttl)
	}
	o.apply(opts...)

	return newMemoryItem(value, o)
}

// getItem retrieves a memory item using the provided getter function and checks for expiration.
//
// This is a helper method that wraps the getter function and adds expiration checking.
// It returns ErrKeyNotFound if the item doesn't exist or has expired.
//
// An expired item is removed from the map before returning, so entries do not
// accumulate for the lifetime of the cache when their keys are never read
// again. The getter releases its lock before returning, so the removal can
// take the write lock it needs.
//
// Parameters:
//   - key: The key the getter reads, used to evict an expired entry
//   - getter: A function that returns a memory item and a boolean indicating if it was found
//
// Returns:
//   - *memoryItem: The memory item if found and not expired
//   - error: nil on success, ErrKeyNotFound if key doesn't exist or has expired
func (m *MemoryCache) getItem(key string, getter func() (*memoryItem, bool)) (*memoryItem, error) {
	item, ok := getter()

	if !ok {
		return nil, ErrKeyNotFound
	}

	if item.isExpired(time.Now()) {
		// The getter may only hold a read lock, so the removal needs its own
		// write lock. A concurrent Set may have replaced the entry in between,
		// so delete only while the map still holds the item this call read.
		m.mux.Lock()
		if current, found := m.items[key]; found && current == item {
			delete(m.items, key)
			m.trackRemove(key)
		}
		m.mux.Unlock()

		return nil, ErrKeyNotFound
	}

	return item, nil
}

// getValue retrieves the value of a memory item using the provided getter function.
//
// This is a helper method that uses getItem to get the memory item and then
// extracts the byte slice value from it.
//
// Parameters:
//   - key: The key the getter reads, used to evict an expired entry
//   - getter: A function that returns a memory item and a boolean indicating if it was found
//
// Returns:
//   - []byte: The cached value if found and not expired
//   - error: nil on success, ErrKeyNotFound if key doesn't exist or has expired
func (m *MemoryCache) getValue(key string, getter func() (*memoryItem, bool)) ([]byte, error) {
	item, err := m.getItem(key, getter)
	if err != nil {
		return nil, err
	}

	return item.value, nil
}

// addInt64 adds a and b, reporting false when the result would overflow or
// underflow the int64 range.
func addInt64(a, b int64) (int64, bool) {
	if (b > 0 && a > math.MaxInt64-b) || (b < 0 && a < math.MinInt64-b) {
		return 0, false
	}

	return a + b, true
}

// cleanup removes all expired items from the memory cache and executes the provided callback.
//
// This method scans through all items in the cache and removes any that have expired.
// The callback function is executed while holding the write lock, allowing for
// atomic operations during cleanup.
//
// Parameters:
//   - cb: A callback function to execute during cleanup (while holding the write lock)
func (m *MemoryCache) cleanup(cb func()) {
	t := time.Now()

	m.mux.Lock()
	for key, item := range m.items {
		if item.isExpired(t) {
			delete(m.items, key)
			m.trackRemove(key)
		}
	}

	cb()
	m.mux.Unlock()
}

// Close releases any resources held by the memory cache.
//
// For the memory cache implementation, this is a no-op since there are no
// external resources to clean up. The method is included for interface compatibility.
//
// Returns:
//   - error: Always nil for memory cache
//
// Example:
//
//	// Properly close the cache when done
//	defer cache.Close()
func (m *MemoryCache) Close() error {
	return nil
}

// Compile-time check to ensure memoryCache implements the Cache interface.
var _ Cache = (*MemoryCache)(nil)
