# Cache Package Function Index

## Exported Functions

### PruneColumns
- **Signature**: `func PruneColumns(ctx context.Context, tsvContent string, stepInstruction string) (string, error)`
- **Description**: Prunes columns from TSV content based on step instructions using LLM guidance.

### Process
- **Signature**: `func Process(ctx context.Context, payload string, stepInstruction string) (processedPayload string, cacheID string, err error)`
- **Description**: Compacts payloads and triggers the full caching workflow if the payload exceeds a threshold.
