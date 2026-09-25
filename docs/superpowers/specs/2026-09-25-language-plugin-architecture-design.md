# Language Adapter Plugin Architecture Design

## 1. Overview
`tzro` currently uses hardcoded language adapters (Go, TypeScript, Python, Rust) embedded within the core binary. To support a wider array of languages—such as Java, Apex, and Lightning Web Components (LWC)—without bloating the core executable, we are introducing a plugin architecture based on HashiCorp's `go-plugin` (gRPC over stdio). 

This allows users to drop custom executable binaries into a plugin directory to support new languages like Kotlin, Dart, or PHP, while keeping the core `tzro` binary zero-dependency.

## 2. Architecture & Plugin Lifecycle

### 2.1 The gRPC Contract
Plugins will communicate with `tzro` via a strict Protocol Buffers gRPC contract implementing the `LanguagePlugin` interface:
- `DetectLanguage(filename string) bool`: Determines if the plugin handles the given file.
- `FindDeclarations(fileContent []byte) []Symbol`: Extracts AST-level declarations from a source file.
- `FindReferences(workspace string, symbol Symbol) []RawReference`: Locates references across the workspace.

### 2.2 Host `PluginManager`
A new `PluginManager` will be added to `pkg/context/`. 
- **Startup**: It scans `~/.tzro/plugins/lang/` for executable binaries and launches them as background child processes.
- **Registration**: It wraps the gRPC client connections in a struct that satisfies the existing internal `ReferenceAdapter` interface, registering them with the `AdapterRegistry`.
- **Lifecycle**: Plugins are kept alive for the duration of the `tzro` daemon. On shutdown, `tzro` issues a kill signal to gracefully terminate the child processes.

## 3. Initial Plugins (Java, Apex, LWC)
To validate the architecture, the first three new languages will be implemented as external plugins maintained within the `tzro` repository under a `plugins/` directory:

1. **Java (`tzro-lang-java`)**: A Go-based plugin utilizing `odvcencio/gotreesitter` (which embeds the Java grammar) to parse `class`, `interface`, `method`, and `variable` declarations.
2. **Apex (`tzro-lang-apex`)**: Since Apex syntax closely mirrors Java, this Go-based plugin will attempt to parse `.cls` and `.trigger` files using the Java tree-sitter grammar. If parsing fails on Apex-specific constructs (like SOQL), it gracefully falls back to robust Regex extraction for classes and triggers.
3. **LWC (`tzro-lang-lwc`)**: Targets the paired `.html`, `.js`, and `.css` files of Lightning Web Components. It will use the existing JS tree-sitter grammar for `.js` logic and a specialized Regex/DOM parser for `.html` templates to map variable bindings.

## 4. Distribution, Error Handling, & Fallbacks

### 4.1 Security and Distribution
Plugins are standalone executable binaries. For security, `tzro` will only load plugins from a highly restricted local directory (`~/.tzro/plugins/lang/`). Users can add third-party language support by placing compatible binaries in this directory.

### 4.2 Fallback Chain
Robust error handling is critical to prevent a failing plugin from halting codebase exploration:
1. If a plugin crashes or panics during a gRPC call (e.g., `FindDeclarations`), the error is caught by the `PluginManager`.
2. `tzro` will gracefully degrade to its built-in `fallbackFindReferencesBatch` (regex/grep search).
3. The crashing plugin will be marked as "degraded" for the session to prevent a continuous crash loop on subsequent files.

### 4.3 Telemetry
The existing `CoverageReport` struct in `pkg/context/impact.go` will be augmented with plugin metadata (e.g., `PluginUsed`, `PluginName`, `PluginErrors`). This surfaces silent plugin failures to the user, ensuring transparency when the system drops down to regex fallbacks.
