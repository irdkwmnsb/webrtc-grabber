package signalling

import "sync"

// SyncMapWrapper provides a type-safe wrapper around sync.Map with generic types.
// It eliminates the need for type assertions when working with sync.Map by
// providing compile-time type safety for keys and values.
//
// sync.Map is optimized for two common use cases:
//  1. When entries are only ever written once but read many times (write-once, read-many)
//  2. When multiple goroutines read, write, and overwrite entries for disjoint sets of keys
//
// In this application, SyncMapWrapper is used for:
//   - publishers map: Keys are strings (publisher keys), values are *Publisher
//   - subscribers map: Keys are strings (socket IDs), values are *Subscriber
//
// The wrapper provides the same thread-safety guarantees as sync.Map while
// offering a more ergonomic and type-safe API.
//
// Type parameters:
//   - K: Key type, must be comparable (can be used with == and !=)
//   - V: Value type, can be any type
type SyncMapWrapper[K comparable, V any] struct {
	// sm is the underlying sync.Map that provides thread-safe operations
	sm sync.Map
}

// NewSyncMapWrapper creates a new empty SyncMapWrapper with the specified key and value types.
//
// Example usage:
//
//	publishers := NewSyncMapWrapper[string, *Publisher]()
//	subscribers := NewSyncMapWrapper[string, *Subscriber]()
func NewSyncMapWrapper[K comparable, V any]() *SyncMapWrapper[K, V] {
	return &SyncMapWrapper[K, V]{}
}

// Store sets the value for a key in the map.
// If the key already exists, the old value is replaced with the new value.
//
// This method is thread-safe and can be called concurrently from multiple goroutines.
func (sw *SyncMapWrapper[K, V]) Store(key K, value V) {
	sw.sm.Store(key, value)
}

// Load retrieves the value associated with a key from the map.
//
// Returns:
//   - value: The value associated with the key, or the zero value of type V if not found
//   - ok: true if the key was present in the map, false otherwise
//
// This method is thread-safe and can be called concurrently from multiple goroutines.
//
// Example:
//
//	publisher, ok := publishers.Load("192.168.1.5:12345_webcam")
//	if !ok {
//	    // Publisher not found
//	}
func (sw *SyncMapWrapper[K, V]) Load(key K) (V, bool) {
	val, ok := sw.sm.Load(key)
	if !ok {
		var zero V
		return zero, false
	}
	return val.(V), true
}

// LoadAndDelete retrieves and deletes the value associated with a key in a single atomic operation.
// This is useful for cleanup operations where you need to process a value before removing it.
//
// Returns:
//   - value: The value that was associated with the key, or zero value if not found
//   - ok: true if the key was present in the map, false otherwise
//
// This method is thread-safe and atomic - the load and delete happen as one operation.
//
// Example:
//
//	subscriber, ok := subscribers.LoadAndDelete("192.168.1.10:54321")
//	if ok {
//	    subscriber.pc.Close() // Clean up before removal
//	}
func (sw *SyncMapWrapper[K, V]) LoadAndDelete(key K) (V, bool) {
	val, ok := sw.sm.LoadAndDelete(key)
	if !ok {
		var zero V
		return zero, false
	}
	return val.(V), true
}

// Delete removes a key and its associated value from the map.
// If the key is not present, Delete is a no-op.
//
// This method is thread-safe and can be called concurrently from multiple goroutines.
func (sw *SyncMapWrapper[K, V]) Delete(key K) {
	sw.sm.Delete(key)
}

// Range calls the provided function for each key-value pair in the map.
// The iteration continues until all entries have been visited or the function returns false.
//
// Range does not correspond to any particular snapshot of the map's contents:
// no key will be visited more than once, but if the value for any key is stored
// or deleted concurrently, Range may reflect any mapping for that key from any
// point during the Range call.
//
// Parameters:
//   - f: Function to call for each entry. Return false to stop iteration early.
//
// This method is thread-safe. The provided function is called while holding
// internal locks, so it should complete quickly and should not call other
// methods on the same SyncMapWrapper to avoid deadlock.
//
// Example:
//
//	publishers.Range(func(key string, publisher *Publisher) bool {
//	    log.Printf("Publisher %s has %d subscribers", key, publisher.BroadcasterCount())
//	    return true // continue iteration
//	})
func (sw *SyncMapWrapper[K, V]) Range(f func(key K, value V) bool) {
	sw.sm.Range(func(key, value interface{}) bool {
		return f(key.(K), value.(V))
	})
}

// Len returns the number of entries currently in the map.
// This operation requires iterating through all entries and may be slow for large maps.
//
// Note: The count may be stale by the time it is returned if other goroutines
// are concurrently modifying the map.
//
// This method is thread-safe but not constant-time (O(n) complexity).
func (sw *SyncMapWrapper[K, V]) Len() int {
	count := 0
	sw.sm.Range(func(key, value interface{}) bool {
		count++
		return true
	})
	return count
}

// Clear removes all entries from the map.
// This is equivalent to creating a new map, but reuses the existing wrapper.
//
// This method is thread-safe but requires iterating through all entries,
// so it may be slow for large maps (O(n) complexity).
//
// Example:
//
//	// During shutdown
//	publishers.Clear()
//	subscribers.Clear()
func (sw *SyncMapWrapper[K, V]) Clear() {
	sw.sm.Range(func(key, value interface{}) bool {
		sw.sm.Delete(key)
		return true
	})
}

// LoadOrStore returns the existing value for the key if present.
// Otherwise, it stores and returns the given value.
// The loaded result is true if the value was loaded, false if stored.
//
// This operation is atomic - either the existing value is loaded OR the new
// value is stored, but not both. This is useful for ensuring only one instance
// of a value exists for a given key.
//
// Returns:
//   - actual: The value now associated with the key (either existing or newly stored)
//   - loaded: true if the value was already present, false if it was just stored
//
// This method is thread-safe and the load-or-store happens atomically.
//
// Example (from ensureGrabberConnection):
//
//	publisher, loaded := pm.publishers.LoadOrStore(publisherKey, NewPublisher())
//	if loaded {
//	    // Publisher already existed, use the existing one
//	} else {
//	    // We just created a new publisher, need to set it up
//	}
func (sw *SyncMapWrapper[K, V]) LoadOrStore(key K, value V) (actual V, loaded bool) {
	val, loaded := sw.sm.LoadOrStore(key, value)
	return val.(V), loaded
}
