# Separate StreamBus for token-level streaming fan-out

LLM inference streaming uses a dedicated `internal/stream` pub/sub bus rather than the existing Observer event channel. The Observer's `ObserverChan` is designed for coarse lifecycle events (task started, node completed) with debounce-based batching. Mixing high-frequency token chunks (hundreds per second) would flood the debouncer and break its aggregation logic.

The StreamBus allows multiple subscribers (HTTP response writer, Observer for aggregate metrics, future modules) to independently consume the same stream without stealing events from each other. Subscribers register with filters (e.g., by stream ID or task ID) and receive copies of each chunk on their own channel. The Observer subscribes for stream-level summaries (completion events, total token counts) rather than individual tokens.

Considered unifying everything through the Observer channel with type tags, but Go channels are single-consumer — fan-out would require a manual dispatcher goroutine on top of the existing debouncer, adding complexity to a system that works well for its current purpose.
