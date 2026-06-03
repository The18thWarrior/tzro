# ADR-0005: 5-Layer Context Compaction & Disk-Backed JQ Cache

## Context & Problem Statement

Modern enterprise workflows involve retrieving massive payloads from third-party systems (such as hundreds of rows from a database, huge chunks of JQL queries from Jira, or complete HTML bodies from web scrapers).

If these payloads are fed directly into the context window of a local worker LLM, they quickly overwhelm the window size. This leads to out-of-memory crashes, context truncation, or severe generation lag.

Even standard JSON data contains massive structural overhead—curly braces, brackets, quotes, and repeated key names—which inflate token usage without adding semantic value.

## Proposed Decision

We choose to implement a high-efficiency **5-Layer Context Compaction Pipeline** to recursively compress inputs, coupled with a **Disk-Backed JQ Cache** for massive payloads.

1. **5-Layer Compaction Pipeline:** All raw tool results are fed through five sequential filters before context injection:
   - **Layer 0 (Base64 Strip):** Identifies base64 string signatures and replaces them with clean metadata envelopes.
   - **Layer 1 (HTML Converter):** Strips raw HTML structures and converts them to readable, semantic Markdown.
   - **Layer 2 (Tabular TSV):** Converts arrays of JSON objects into Tabular TSV with a single header row, stripping all repeated key syntax.
   - **Layer 3 (KV Formatter):** Replaces single JSON object brackets with a list of clean `key: value` lines.
   - **Layer 4 (Dot Path Flatten):** Flattens nested JSON hierarchies into dot notation (e.g., `user.profile.address.zip: 94016`) up to 3 hops deep.
2. **Disk-Backed JQ Cache Gateway:** If the payload remains larger than **12KB** after compaction, it is saved to an on-disk SQLite cache database.
3. **Cache Envelopes:** Instead of the full payload, the model receives a compact metadata envelope.
4. **Cache Exploration Guide:** The engine injects instructions guiding the local worker LLM to query the on-disk cache using specific tools like `jq_cached_data`.

---

## Technical Specifications

### 1. The 5-Layer Compaction Pipeline Flow

```
                      Raw Verbose Tool Result
                                 │
                                 ▼
┌──────────────────────────────────────────────────────────────┐
│  Layer 0: Strip Base64 Binary                                │
│  - Replaces raw byte streams with: [binary:image/png,1.2MB]  │
└──────────────────────────────────────────────────────────────┘
                                 │
                                 ▼
┌──────────────────────────────────────────────────────────────┐
│  Layer 1: Convert HTML to Markdown                           │
│  - Replaces <div><span> with plaintext markdown formatting   │
└──────────────────────────────────────────────────────────────┘
                                 │
                                 ▼
┌──────────────────────────────────────────────────────────────┐
│  Layer 2: Tabular JSON arrays to TSV                         │
│  - Converts [{"id": 1, "name": "A"}] to: id \t name \n 1 \t A │
└──────────────────────────────────────────────────────────────┘
                                 │
                                 ▼
┌──────────────────────────────────────────────────────────────┐
│  Layer 3: Single JSON Object to Key:Value lines              │
│  - Replaces quotes, brackets, and braces                     │
└──────────────────────────────────────────────────────────────┘
                                 │
                                 ▼
┌──────────────────────────────────────────────────────────────┐
│  Layer 4: Flatten Nested Hierarchies to Dot-Notation         │
│  - Replaces deep trees with paths: user.profile.role: admin  │
└──────────────────────────────────────────────────────────────┘
```

---

### 2. Disk-Backed JQ Cache Envelope Schema

If a compacted result exceeds 12KB, the executor saves the full data to SQLite and outputs this lightweight JSON envelope:

```json
{
  "cacheId": "9fbb5166-7935-493a-ba",
  "dataType": "array",
  "rootPath": ".records",
  "fields": ["Id", "Name", "Email", "Amount"],
  "fieldTypes": {
    "Id": "string",
    "Name": "string",
    "Amount": "number"
  },
  "enumValues": {
    "Status": ["Closed Won", "Prospecting"]
  },
  "sampleRecord": {
    "Id": "0038W00001zKx4zQAC",
    "Name": "John Doe",
    "Amount": 15000
  }
}
```

---

### 3. Cache Exploration Guide Prompt Injection

When a JQ cache envelope is returned, the following instruction tutorial is appended to the agent's prompt context:

```
## CACHED DATA EXPLORATION
A tool result was too large and has been cached on disk. You received a "cacheId".
To query this data without exceeding context limits, use the following tools:
1. introspect_cache — deep nested schema analysis
2. read_cached_data — paginated offset reads
3. jq_cached_data   — run targeted JQ query expressions

CRITICAL: Always use the "rootPath" from the envelope.
- If rootPath is ".records", JQ starts with: .records[] | select(.Amount > 500)
- NEVER assume .[] when rootPath is populated.
```

---

## Consequences

- **Pros:**
  - **Exceptional Context Savings:** Tabular JSON-to-TSV conversion typically reduces token overhead by **60% to 80%** without losing semantic mapping.
  - **Infinite Scale:** Large payloads can be queried on-disk dynamically, enabling agents to handle megabyte-sized files on tiny local context windows.
  - **Protects Local VRAM:** Keeps local inference fast by maintaining highly compact context payloads.
- **Cons:**
  - **Query Complexity:** Local LLMs must learn to coordinate JQ queries when given Cache Envelopes, increasing the number of tool hops for large datasets.
  - **Disk Overhead:** Generates local temporary disk writes; requires periodic background cache purges to free local drive space.

#
