package codegen

import (
	"strings"
)

// LanguageExemplars returns short, canonical code snippets demonstrating
// concurrency primitives and advanced type patterns for the given language.
// Returns empty string if the spec does not mention relevant keywords —
// exemplars are only injected when the task requires these patterns.
//
// This helps the local model (which lacks deep implicit knowledge of
// concurrency primitives and advanced generics) produce correct code for
// mutex-protected caches, generic containers, discriminated unions, etc.
func LanguageExemplars(language, spec string) string {
	specLower := strings.ToLower(spec)
	needsConcurrency := containsAny(specLower,
		"mutex", "lock", "concurrent", "goroutine", "thread-safe", "sync.", "rwmutex", "channel",
		"async", "await", "synchronized", "volatile", "atomic", "semaphore", "parallel",
		"std::mutex", "std::thread", "arc<", "tokio", "task.run",
	)
	needsGenerics := containsAny(specLower,
		"generic", "type parameter", "discriminated union", "mapped type", "interface{", "any",
		"comparable", "constraints", "template", "where clause", "bounded type", "enum variant",
	)
	needsValidation := containsAny(specLower,
		"validate", "validation", "validator", "contains '@'", "contains \"@\"", "check id", "checking id",
	)

	if !needsConcurrency && !needsGenerics && !needsValidation {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("## Reference Patterns\n")
	sb.WriteString("Use these canonical patterns as reference for correct syntax.\n\n")

	switch language {
	case "go":
		if needsConcurrency {
			sb.WriteString("### Go Concurrency (sync.RWMutex)\n")
			sb.WriteString("```go\n")
			sb.WriteString("type SafeMap struct {\n")
			sb.WriteString("\tmu    sync.RWMutex\n")
			sb.WriteString("\titems map[string]any\n")
			sb.WriteString("}\n\n")
			sb.WriteString("func (m *SafeMap) Get(key string) (any, bool) {\n")
			sb.WriteString("\tm.mu.RLock()\n")
			sb.WriteString("\tdefer m.mu.RUnlock()\n")
			sb.WriteString("\tv, ok := m.items[key]\n")
			sb.WriteString("\treturn v, ok\n")
			sb.WriteString("}\n\n")
			sb.WriteString("func (m *SafeMap) Set(key string, val any) {\n")
			sb.WriteString("\tm.mu.Lock()\n")
			sb.WriteString("\tdefer m.mu.Unlock()\n")
			sb.WriteString("\tm.items[key] = val\n")
			sb.WriteString("}\n")
			sb.WriteString("```\n\n")
			sb.WriteString("Key rules: RLock for reads, Lock for writes. Never write under RLock. Always defer Unlock.\n\n")
		}
		if needsGenerics {
			sb.WriteString("### Go Generics (type parameters)\n")
			sb.WriteString("```go\n")
			sb.WriteString("type Cache[K comparable, V any] struct {\n")
			sb.WriteString("\tentries map[K]V\n")
			sb.WriteString("}\n\n")
			sb.WriteString("func NewCache[K comparable, V any]() *Cache[K, V] {\n")
			sb.WriteString("\treturn &Cache[K, V]{entries: make(map[K]V)}\n")
			sb.WriteString("}\n")
			sb.WriteString("```\n\n")
		}
		if needsValidation {
			sb.WriteString("### Go Validation & String Checks\n")
			sb.WriteString("```go\n")
			sb.WriteString("import (\n\t\"fmt\"\n\t\"strings\"\n)\n\n")
			sb.WriteString("// Use strings.Contains for substring checks (e.g. '@' in email)\n")
			sb.WriteString("if u.Email == \"\" || !strings.Contains(u.Email, \"@\") {\n")
			sb.WriteString("\treturn fmt.Errorf(\"email must contain '@'\")\n")
			sb.WriteString("}\n")
			sb.WriteString("```\n\n")
		}
	case "typescript":
		if needsConcurrency {
			sb.WriteString("### TypeScript Async Patterns\n")
			sb.WriteString("```typescript\n")
			sb.WriteString("async function sendRequest(url: string, body: object): Promise<Result> {\n")
			sb.WriteString("  const response = await fetch(url, {\n")
			sb.WriteString("    method: 'POST',\n")
			sb.WriteString("    headers: { 'Content-Type': 'application/json' },\n")
			sb.WriteString("    body: JSON.stringify(body),\n")
			sb.WriteString("  });\n")
			sb.WriteString("  if (!response.ok) {\n")
			sb.WriteString("    return { success: false, error: response.statusText };\n")
			sb.WriteString("  }\n")
			sb.WriteString("  const data = await response.json();\n")
			sb.WriteString("  return { success: true, data };\n")
			sb.WriteString("}\n")
			sb.WriteString("```\n\n")
		}
		if needsGenerics {
			sb.WriteString("### TypeScript Discriminated Unions & Generics\n")
			sb.WriteString("```typescript\n")
			sb.WriteString("// Discriminated union with type narrowing\n")
			sb.WriteString("type Shape =\n")
			sb.WriteString("  | { kind: 'circle'; radius: number }\n")
			sb.WriteString("  | { kind: 'rect'; width: number; height: number };\n\n")
			sb.WriteString("function area(s: Shape): number {\n")
			sb.WriteString("  switch (s.kind) {\n")
			sb.WriteString("    case 'circle': return Math.PI * s.radius ** 2;\n")
			sb.WriteString("    case 'rect': return s.width * s.height;\n")
			sb.WriteString("  }\n")
			sb.WriteString("}\n\n")
			sb.WriteString("// Generic event emitter\n")
			sb.WriteString("type EventMap = Record<string, unknown[]>;\n")
			sb.WriteString("type Listener<Args extends unknown[]> = (...args: Args) => void;\n\n")
			sb.WriteString("class Emitter<Events extends EventMap> {\n")
			sb.WriteString("  private listeners = new Map<keyof Events, Set<Listener<any>>>();\n\n")
			sb.WriteString("  on<K extends keyof Events>(event: K, fn: Listener<Events[K]>): void {\n")
			sb.WriteString("    if (!this.listeners.has(event)) this.listeners.set(event, new Set());\n")
			sb.WriteString("    this.listeners.get(event)!.add(fn);\n")
			sb.WriteString("  }\n\n")
			sb.WriteString("  emit<K extends keyof Events>(event: K, ...args: Events[K]): void {\n")
			sb.WriteString("    this.listeners.get(event)?.forEach(fn => fn(...args));\n")
			sb.WriteString("  }\n")
			sb.WriteString("}\n")
			sb.WriteString("```\n\n")
		}
	case "javascript":
		if needsConcurrency {
			sb.WriteString("### JavaScript Async/Await\n")
			sb.WriteString("```javascript\n")
			sb.WriteString("async function fetchWithRetry(url, options = {}, retries = 3) {\n")
			sb.WriteString("  for (let attempt = 0; attempt < retries; attempt++) {\n")
			sb.WriteString("    try {\n")
			sb.WriteString("      const response = await fetch(url, options);\n")
			sb.WriteString("      if (!response.ok) throw new Error(`HTTP ${response.status}`);\n")
			sb.WriteString("      return await response.json();\n")
			sb.WriteString("    } catch (err) {\n")
			sb.WriteString("      if (attempt === retries - 1) throw err;\n")
			sb.WriteString("      await new Promise(r => setTimeout(r, 1000 * 2 ** attempt));\n")
			sb.WriteString("    }\n")
			sb.WriteString("  }\n")
			sb.WriteString("}\n\n")
			sb.WriteString("// Promise.allSettled for parallel tasks\n")
			sb.WriteString("const results = await Promise.allSettled([\n")
			sb.WriteString("  fetchData('/api/users'),\n")
			sb.WriteString("  fetchData('/api/posts'),\n")
			sb.WriteString("]);\n")
			sb.WriteString("const succeeded = results.filter(r => r.status === 'fulfilled').map(r => r.value);\n")
			sb.WriteString("const failed = results.filter(r => r.status === 'rejected').map(r => r.reason);\n")
			sb.WriteString("```\n\n")
		}
	case "java":
		if needsConcurrency {
			sb.WriteString("### Java Concurrency (ReadWriteLock, ConcurrentHashMap)\n")
			sb.WriteString("```java\n")
			sb.WriteString("import java.util.concurrent.locks.ReadWriteLock;\n")
			sb.WriteString("import java.util.concurrent.locks.ReentrantReadWriteLock;\n\n")
			sb.WriteString("public class SafeCache<K, V> {\n")
			sb.WriteString("    private final ReadWriteLock lock = new ReentrantReadWriteLock();\n")
			sb.WriteString("    private final Map<K, V> map = new HashMap<>();\n\n")
			sb.WriteString("    public V get(K key) {\n")
			sb.WriteString("        lock.readLock().lock();\n")
			sb.WriteString("        try {\n")
			sb.WriteString("            return map.get(key);\n")
			sb.WriteString("        } finally {\n")
			sb.WriteString("            lock.readLock().unlock();\n")
			sb.WriteString("        }\n")
			sb.WriteString("    }\n\n")
			sb.WriteString("    public void put(K key, V value) {\n")
			sb.WriteString("        lock.writeLock().lock();\n")
			sb.WriteString("        try {\n")
			sb.WriteString("            map.put(key, value);\n")
			sb.WriteString("        } finally {\n")
			sb.WriteString("            lock.writeLock().unlock();\n")
			sb.WriteString("        }\n")
			sb.WriteString("    }\n")
			sb.WriteString("}\n")
			sb.WriteString("```\n\n")
			sb.WriteString("Key rules: Always unlock in `finally`. Prefer `ReentrantReadWriteLock` over `synchronized` for read-heavy access. Use `ConcurrentHashMap` for simple cases.\n\n")
		}
		if needsGenerics {
			sb.WriteString("### Java Generics (bounded type parameters)\n")
			sb.WriteString("```java\n")
			sb.WriteString("// Bounded type parameter with Comparable\n")
			sb.WriteString("public class SortedList<T extends Comparable<T>> {\n")
			sb.WriteString("    private final List<T> items = new ArrayList<>();\n\n")
			sb.WriteString("    public void add(T item) {\n")
			sb.WriteString("        items.add(item);\n")
			sb.WriteString("        Collections.sort(items);\n")
			sb.WriteString("    }\n\n")
			sb.WriteString("    public <R> List<R> map(Function<T, R> mapper) {\n")
			sb.WriteString("        return items.stream().map(mapper).collect(Collectors.toList());\n")
			sb.WriteString("    }\n")
			sb.WriteString("}\n\n")
			sb.WriteString("// Sealed interface (Java 17+)\n")
			sb.WriteString("public sealed interface Result<T> permits Success, Failure {\n")
			sb.WriteString("    record Success<T>(T value) implements Result<T> {}\n")
			sb.WriteString("    record Failure<T>(String error) implements Result<T> {}\n")
			sb.WriteString("}\n")
			sb.WriteString("```\n\n")
		}
	case "cpp":
		if needsConcurrency {
			sb.WriteString("### C++ Concurrency (std::shared_mutex, RAII locks)\n")
			sb.WriteString("```cpp\n")
			sb.WriteString("#include <shared_mutex>\n")
			sb.WriteString("#include <unordered_map>\n")
			sb.WriteString("#include <optional>\n\n")
			sb.WriteString("template<typename K, typename V>\n")
			sb.WriteString("class ThreadSafeMap {\n")
			sb.WriteString("    mutable std::shared_mutex mutex_;\n")
			sb.WriteString("    std::unordered_map<K, V> map_;\n")
			sb.WriteString("public:\n")
			sb.WriteString("    std::optional<V> get(const K& key) const {\n")
			sb.WriteString("        std::shared_lock lock(mutex_);\n")
			sb.WriteString("        auto it = map_.find(key);\n")
			sb.WriteString("        return it != map_.end() ? std::optional(it->second) : std::nullopt;\n")
			sb.WriteString("    }\n\n")
			sb.WriteString("    void set(const K& key, V value) {\n")
			sb.WriteString("        std::unique_lock lock(mutex_);\n")
			sb.WriteString("        map_[key] = std::move(value);\n")
			sb.WriteString("    }\n")
			sb.WriteString("};\n")
			sb.WriteString("```\n\n")
			sb.WriteString("Key rules: `std::shared_lock` for reads, `std::unique_lock` for writes. Mark mutex `mutable` for const methods. Use RAII locks — never manual lock/unlock.\n\n")
		}
		if needsGenerics {
			sb.WriteString("### C++ Templates & Concepts (C++20)\n")
			sb.WriteString("```cpp\n")
			sb.WriteString("#include <concepts>\n")
			sb.WriteString("#include <variant>\n\n")
			sb.WriteString("template<typename T>\n")
			sb.WriteString("concept Hashable = requires(T a) {\n")
			sb.WriteString("    { std::hash<T>{}(a) } -> std::convertible_to<std::size_t>;\n")
			sb.WriteString("};\n\n")
			sb.WriteString("template<Hashable K, typename V>\n")
			sb.WriteString("class Cache {\n")
			sb.WriteString("    std::unordered_map<K, V> entries_;\n")
			sb.WriteString("public:\n")
			sb.WriteString("    void insert(const K& key, V value) { entries_[key] = std::move(value); }\n")
			sb.WriteString("    std::optional<V> lookup(const K& key) const {\n")
			sb.WriteString("        auto it = entries_.find(key);\n")
			sb.WriteString("        return it != entries_.end() ? std::optional(it->second) : std::nullopt;\n")
			sb.WriteString("    }\n")
			sb.WriteString("};\n\n")
			sb.WriteString("// std::variant as discriminated union\n")
			sb.WriteString("using Shape = std::variant<Circle, Rectangle, Triangle>;\n")
			sb.WriteString("double area(const Shape& s) {\n")
			sb.WriteString("    return std::visit([](const auto& shape) { return shape.area(); }, s);\n")
			sb.WriteString("}\n")
			sb.WriteString("```\n\n")
		}
	case "csharp":
		if needsConcurrency {
			sb.WriteString("### C# Concurrency (async/await, ConcurrentDictionary)\n")
			sb.WriteString("```csharp\n")
			sb.WriteString("using System.Collections.Concurrent;\n\n")
			sb.WriteString("public class SafeCache<TKey, TValue> where TKey : notnull\n")
			sb.WriteString("{\n")
			sb.WriteString("    private readonly ConcurrentDictionary<TKey, TValue> _cache = new();\n\n")
			sb.WriteString("    public TValue GetOrAdd(TKey key, Func<TKey, TValue> factory)\n")
			sb.WriteString("        => _cache.GetOrAdd(key, factory);\n\n")
			sb.WriteString("    public bool TryGet(TKey key, out TValue? value)\n")
			sb.WriteString("        => _cache.TryGetValue(key, out value);\n")
			sb.WriteString("}\n\n")
			sb.WriteString("// Async with cancellation\n")
			sb.WriteString("public async Task<Result> FetchAsync(string url, CancellationToken ct = default)\n")
			sb.WriteString("{\n")
			sb.WriteString("    using var client = new HttpClient();\n")
			sb.WriteString("    var response = await client.GetAsync(url, ct);\n")
			sb.WriteString("    response.EnsureSuccessStatusCode();\n")
			sb.WriteString("    var body = await response.Content.ReadAsStringAsync(ct);\n")
			sb.WriteString("    return new Result(true, body);\n")
			sb.WriteString("}\n")
			sb.WriteString("```\n\n")
			sb.WriteString("Key rules: Prefer `ConcurrentDictionary` over `lock` + `Dictionary`. Always accept `CancellationToken`. Use `using` for disposable resources.\n\n")
		}
		if needsGenerics {
			sb.WriteString("### C# Generics (constraints, records, pattern matching)\n")
			sb.WriteString("```csharp\n")
			sb.WriteString("// Generic with where constraints\n")
			sb.WriteString("public class Repository<T> where T : class, IEntity, new()\n")
			sb.WriteString("{\n")
			sb.WriteString("    private readonly List<T> _items = new();\n")
			sb.WriteString("    public T? FindById(Guid id) => _items.FirstOrDefault(x => x.Id == id);\n")
			sb.WriteString("    public void Add(T item) => _items.Add(item);\n")
			sb.WriteString("}\n\n")
			sb.WriteString("// Discriminated union via abstract record\n")
			sb.WriteString("public abstract record Shape;\n")
			sb.WriteString("public record Circle(double Radius) : Shape;\n")
			sb.WriteString("public record Rectangle(double Width, double Height) : Shape;\n\n")
			sb.WriteString("public static double Area(Shape shape) => shape switch\n")
			sb.WriteString("{\n")
			sb.WriteString("    Circle c => Math.PI * c.Radius * c.Radius,\n")
			sb.WriteString("    Rectangle r => r.Width * r.Height,\n")
			sb.WriteString("    _ => throw new ArgumentException(\"Unknown shape\")\n")
			sb.WriteString("};\n")
			sb.WriteString("```\n\n")
		}
	case "rust":
		if needsConcurrency {
			sb.WriteString("### Rust Concurrency (Arc, RwLock)\n")
			sb.WriteString("```rust\n")
			sb.WriteString("use std::collections::HashMap;\n")
			sb.WriteString("use std::sync::{Arc, RwLock};\n\n")
			sb.WriteString("type SharedMap<K, V> = Arc<RwLock<HashMap<K, V>>>;\n\n")
			sb.WriteString("fn get<K: Eq + std::hash::Hash, V: Clone>(map: &SharedMap<K, V>, key: &K) -> Option<V> {\n")
			sb.WriteString("    let guard = map.read().unwrap();\n")
			sb.WriteString("    guard.get(key).cloned()\n")
			sb.WriteString("}\n\n")
			sb.WriteString("fn set<K: Eq + std::hash::Hash, V>(map: &SharedMap<K, V>, key: K, value: V) {\n")
			sb.WriteString("    let mut guard = map.write().unwrap();\n")
			sb.WriteString("    guard.insert(key, value);\n")
			sb.WriteString("}\n")
			sb.WriteString("```\n\n")
			sb.WriteString("Key rules: `Arc` for shared ownership across threads. `RwLock::read()` for shared reads, `RwLock::write()` for exclusive writes. Use `mpsc` channels for message passing.\n\n")
		}
		if needsGenerics {
			sb.WriteString("### Rust Generics (trait bounds, enum discriminated unions)\n")
			sb.WriteString("```rust\n")
			sb.WriteString("use std::collections::HashMap;\n")
			sb.WriteString("use std::fmt::Display;\n\n")
			sb.WriteString("struct Cache<K: Eq + std::hash::Hash, V> {\n")
			sb.WriteString("    entries: HashMap<K, V>,\n")
			sb.WriteString("}\n\n")
			sb.WriteString("impl<K: Eq + std::hash::Hash, V> Cache<K, V> {\n")
			sb.WriteString("    fn new() -> Self { Cache { entries: HashMap::new() } }\n")
			sb.WriteString("    fn insert(&mut self, key: K, value: V) { self.entries.insert(key, value); }\n")
			sb.WriteString("    fn get(&self, key: &K) -> Option<&V> { self.entries.get(key) }\n")
			sb.WriteString("}\n\n")
			sb.WriteString("// Enum as discriminated union\n")
			sb.WriteString("enum Shape {\n")
			sb.WriteString("    Circle { radius: f64 },\n")
			sb.WriteString("    Rectangle { width: f64, height: f64 },\n")
			sb.WriteString("}\n\n")
			sb.WriteString("fn area(shape: &Shape) -> f64 {\n")
			sb.WriteString("    match shape {\n")
			sb.WriteString("        Shape::Circle { radius } => std::f64::consts::PI * radius * radius,\n")
			sb.WriteString("        Shape::Rectangle { width, height } => width * height,\n")
			sb.WriteString("    }\n")
			sb.WriteString("}\n")
			sb.WriteString("```\n\n")
		}
	case "python":
		if needsConcurrency {
			sb.WriteString("### Python Threading\n")
			sb.WriteString("```python\n")
			sb.WriteString("import threading\n\n")
			sb.WriteString("class ThreadSafeDict:\n")
			sb.WriteString("    def __init__(self):\n")
			sb.WriteString("        self._data = {}\n")
			sb.WriteString("        self._lock = threading.RLock()\n\n")
			sb.WriteString("    def get(self, key, default=None):\n")
			sb.WriteString("        with self._lock:\n")
			sb.WriteString("            return self._data.get(key, default)\n\n")
			sb.WriteString("    def set(self, key, value):\n")
			sb.WriteString("        with self._lock:\n")
			sb.WriteString("            self._data[key] = value\n")
			sb.WriteString("```\n\n")
		}
	}

	result := sb.String()
	if result == "## Reference Patterns\nUse these canonical patterns as reference for correct syntax.\n\n" {
		return "" // No exemplars for this language/spec combo
	}
	return result
}

// containsAny returns true if s contains any of the given substrings.
func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}
