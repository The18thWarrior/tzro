# Deep Modules

From "A Philosophy of Software Design":

**Deep module** = small interface + lots of implementation

```
┌─────────────────────┐
│   Small Interface   │  ← Few methods, simple params
├─────────────────────┤
│                     │
│                     │
│  Deep Implementation│  ← Complex logic hidden
│                     │
│                     │
└─────────────────────┘
```

**Shallow module** = large interface + little implementation (avoid)

```
┌─────────────────────────────────┐
│       Large Interface           │  ← Many methods, complex params
├─────────────────────────────────┤
│  Thin Implementation            │  ← Just passes through
└─────────────────────────────────┘
```

When designing interfaces during planning, ask:

- Can I reduce the number of methods?
- Can I simplify the parameters?
- Can I hide more complexity inside?
- Will the consumer need to understand internals to use this?

## Planning Implications

During plan decomposition, watch for signs of shallow modules:

- **Many small types with trivial logic** — consider merging behind a richer interface
- **Config structs with 10+ fields** — the interface is too wide, find meaningful defaults
- **Wrapper functions that just delegate** — the abstraction isn't earning its keep
