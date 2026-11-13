package cache

// LRUOrder returns the keys the cache tracks in its recency order, most
// recently used first. An unbounded cache returns nil.
func (m *MemoryCache) LRUOrder() []string {
	m.mux.RLock()
	defer m.mux.RUnlock()

	if m.order == nil {
		return nil
	}

	keys := make([]string, 0, m.order.Len())
	for elem := m.order.Front(); elem != nil; elem = elem.Next() {
		keys = append(keys, elem.Value.(string))
	}

	return keys
}
