# Use Case: Relational Knowledge Graph Memory Traversal

**Actor**: Agent or Developer searching for contextual entity relationships.
**Route**: Memory inspector in CLI / web memory graph canvas
**Backend**: http://localhost:36888/api/memory
**Priority**: P1

---

## Intent

A developer wants to search the local Relational Knowledge Graph memory database using hybrid vector search (keyword query + local ONNX embedding similarity) to discover linked entity relations via multi-hop neighborhood search.

## Preconditions

- The SQLite SQLite database is initialized with relational tables (`entities`, `relationships`).
- Local ONNX embeddings model is loaded.
- At least 10 memory nodes with interlinked relationship edges exist.

## Success Criteria

- [ ] Developer sees the candidate node pool filtered using hybrid vector cosine similarity matching.
- [ ] Developer can query memories and retrieve multi-hop context (entities, types, edge strengths, descriptions).
- [ ] Developer sees the neighborhood traversal successfully traverse 1-hop, 2-hop, and 3-hop relations.
- [ ] User can view the interactive node-edge graph structure in real-time.
- [ ] Storing new memories automatically creates entities and links them based on semantic relations.
- [ ] Querying memories returns results with low latency (under 100ms for typical local databases).

## Edge Cases to Probe

- Performing a search when the memory database is completely empty (empty candidate pool).
- Searching for highly ambiguous terms to verify ONNX cosine similarity ranking.
- Navigating dense entity networks (nodes with over 100 relationship edges) to verify neighborhood hop capping.
- Storing a duplicate entity node to verify semantic merging logic.

## Anti-Patterns to Watch For

- [ ] Querying memories locks the SQLite database or blocks all other agent execution.
- [ ] Traversal loops infinitely or memory allocation balloons due to cyclic relationships.
- [ ] Missing relationship edges or orphan nodes appearing without corresponding reference entities.
- [ ] Inaccurate cosine similarity scores outside the normal `[-1, 1]` range.
