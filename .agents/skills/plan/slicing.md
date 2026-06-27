# Vertical Slicing Rules

## What is a vertical slice?

A vertical slice cuts through every layer of the system for ONE narrow behavior. It's the opposite of building "all the types, then all the logic, then all the tests."

```
WRONG (horizontal):
  Layer 1: Define all types/structs
  Layer 2: Write all business logic
  Layer 3: Wire all endpoints
  Layer 4: Write all tests

RIGHT (vertical):
  Slice 1: One type + its logic + its endpoint + its test
  Slice 2: Next type + its logic + its endpoint + its test
  ...
```

## Slice ordering

1. **Tracer bullet first** — the thinnest slice that proves the integration path works end-to-end. Often: one struct, one function, one test. This proves the wiring before you build on it.

2. **Dependency order** — if slice B calls code from slice A, A comes first.

3. **Core → edge** — happy path behaviors first, error handling and edge cases after.

4. **Risk-first** — if one slice has high technical risk (unclear API, new dependency), move it earlier to fail fast.

## How to know if a slice is too thick

- It touches more than 2-3 files to implement
- Its test setup requires mocking multiple collaborators
- You can't explain what it does in one sentence
- It takes more than one TDD cycle (RED→GREEN) to complete

## How to know if a slice is too thin

- It doesn't produce any observable behavior change
- It's just a type definition with no logic
- You can't write a meaningful test for it

## Examples

### Good slices for "add caching to file reader"

```
Slice 1: FileCache stores and retrieves a single entry
  RED:   cache.Get("key") returns miss, cache.Put("key", data), cache.Get("key") returns hit
  GREEN: In-memory map with Get/Put

Slice 2: FileReader uses cache before reading disk
  RED:   reader.Read("file.txt") twice — second call doesn't touch disk
  GREEN: Wire FileCache into FileReader

Slice 3: Cache respects TTL
  RED:   Put with TTL, wait, Get returns miss
  GREEN: Add expiry tracking to cache entries

Slice 4: Cache evicts when full
  RED:   Put N+1 entries into cache with max size N, oldest evicted
  GREEN: LRU eviction on Put
```

### Bad slices (horizontal)

```
Slice 1: Define CacheEntry, FileCache, CacheConfig types
Slice 2: Implement all cache methods
Slice 3: Write all tests
Slice 4: Wire into FileReader
```

The horizontal version writes types without tests, tests without code, and defers integration to the end — where surprises live.
