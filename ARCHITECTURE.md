# Architecture Documentation: `internal` Directory

## Overview

The `internal` directory is the core architectural foundation of the repository, containing 33 subdirectories organized into distinct architectural layers. Each layer serves a specific domain, with clear separation of concerns and well-defined responsibilities. The directory structure reflects a modular, service-oriented design pattern common in modern distributed systems.

---

## Directory Structure

### Core Architectural Layers

#### 1. **Agent & Execution**

| Directory | Responsibility |
|---|---|
| `agent` | Primary orchestrator; manages workflow spawning and execution |
| `executor` | Executes spawned workflows and manages task lifecycles |
| `compiler` | Compiles workflow definitions into executable units |
| `channel` | Manages inter-process communication and message routing |
| `workflow_spawn` | Handles the creation and instantiation of workflow instances |

#### 2. **Data & Storage**

| Directory | Responsibility |
|---|---|
| `db` | Persistent database operations and schema management |
| `cache` | Caching layers for read optimization and memory reduction |
| `memory` | In-memory data structures and temporary storage |
| `embeddings` | Vector embeddings and similarity search operations |

#### 3. **Configuration & Management**

| Directory | Responsibility |
|---|---|
| `config` | System configuration, environment variables, and feature flags |
| `packagemanager` | Package installation, dependency resolution, and lifecycle management |

#### 4. **Observability & Communication**

| Directory | Responsibility |
|---|---|
| `observer` | System monitoring, metrics collection, and health checks |
| `mcp` | Model Context Protocol implementation for AI interactions |
| `notification` | Alerting, event broadcasting, and notification dispatch |

#### 5. **AI & Intelligence**

| Directory | Responsibility |
|---|---|
| `inference` | AI model inference, generation, and prediction operations |
| `classifier` | Text/feature classification and categorization logic |

#### 6. **Performance & Tools**

| Directory | Responsibility |
|---|---|
| `benchmark` | Performance testing, load testing, and benchmarking suites |
| `codegen` | Code generation, scaffolding, and dynamic code creation |

#### 7. **Infrastructure**

| Directory | Responsibility |
|---|---|
| `macronodes` | Macro-level node orchestration, cluster management, and distributed execution |

---

## Key Abstractions and Interactions

### Agent Module (`internal/agent`)
The agent module serves as the central orchestrator of the system. It coordinates workflow spawning, manages task execution lifecycles, and provides the primary interface for invoking business logic. Key components include:
- **Agent**: Main orchestrator that spawns workflows and tracks execution state
- **Workflow Spawn**: Handles the creation of workflow instances from definitions

### Configuration & Package Management (`internal/config`, `internal/packagemanager`)
These modules handle system setup and dependency management:
- **Config**: Manages configuration files, environment variables, and feature flags
- **Packagemanager**: Handles package installation, dependency resolution, and version management

### Data Persistence (`internal/db`, `internal/cache`, `internal/memory`, `internal/embeddings`)
The data layer provides both persistent and transient storage:
- **DB**: Persistent database operations and schema management
- **Cache**: Caching layers for read optimization
- **Memory**: In-memory data structures for temporary storage
- **Embeddings**: Vector embeddings and similarity search capabilities

### Observability (`internal/observer`, `internal/mcp`, `internal/notification`)
The observability layer ensures system transparency:
- **Observer**: System monitoring, metrics collection, and health checks
- **MCP**: Model Context Protocol implementation for AI interactions
- **Notification**: Alerting, event broadcasting, and notification dispatch

### AI Capabilities (`internal/inference`, `internal/classifier`)
The AI layer provides intelligence:
- **Inference**: AI model inference, generation, and prediction operations
- **Classifier**: Text/feature classification and categorization logic

---

## Data Flow Overview

1. **Configuration Load**: `packagemanager` installs dependencies → `config` loads system configuration
2. **Workflow Execution**: `agent` spawns workflows → `compiler` compiles definitions → `executor` runs tasks → `channel` routes messages
3. **AI Processing**: `classifier` categorizes input → `inference` generates predictions → `mcp` handles model context
4. **Data Storage**: Operations flow through `db` (persistent), `cache` (optimized), and `memory` (temporary)
5. **Observability**: All operations are tracked by `observer`, with AI interactions managed through `mcp` and `notification`

---

## Architectural Principles

- **Separation of Concerns**: Each directory represents a distinct architectural layer with well-defined boundaries
- **Modular Design**: Components can be developed, tested, and deployed independently
- **Clear Ownership**: Each directory has a single, well-defined responsibility
- **Interlayer Communication**: Well-defined interfaces between layers enable loose coupling

---

## Summary

The `internal` directory represents a highly organized, service-oriented architecture with 7 distinct architectural layers. The agent module serves as the central orchestrator, while configuration and package management handle system setup. Data flows through persistent and transient storage layers, with observability and AI capabilities providing intelligence and transparency. The clear separation of concerns and modular design enable maintainability, testability, and independent deployment of components.