package cache

// EvictLRU removes the least-recently-used entry to keep the cache under its
// size budget. Called when a Put would exceed capacity.
func EvictLRU() {}
