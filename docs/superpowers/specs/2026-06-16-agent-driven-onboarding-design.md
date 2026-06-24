# Design Spec: Agent-Driven Onboarding & llms.txt Integration

This document outlines the architecture and implementation design for zero-terminal tzro installation, as well as providing an `llms.txt` configuration for LLMs discovering the platform.

## 1. Context & Goals
* **Goal**: Provide a completely automated, non-interactive installation path for non-technical users and AI agents.
* **Goal**: Enable web-searching LLMs to easily discover and parse the onboarding details via a static `llms.txt` file at the website root.

## 2. Proposed Changes

### `website/llms.txt` [NEW]
A static text file containing clear description, setup instructions, and guidelines for LLMs. Since the production build is a static site hosted on GitHub Pages (configured via `.github/workflows/deploy-pages.yml`), placing `llms.txt` directly under `website/` serves it at the root `/llms.txt` in both local Go development and production.

### `.agents/skills/tzro-onboarding/scripts/onboard.sh` [MODIFY]
An automated bash script that executes `install.sh` and `install_plugins.sh` non-interactively using pre-seeded inputs, then appends the necessary binary export paths to the user's active shell configuration (`~/.zshrc` or `~/.bashrc`).

### `.agents/skills/tzro-onboarding/SKILL.md` [MODIFY]
Instructs agents on how to discover missing installations and run `onboard.sh` on behalf of the user.

## 3. Verification Plan
* **Local Website Check**: Verify that `http://localhost:8080/llms.txt` loads the static text file when the Go server is running.
* **Onboarding Test**: Run `onboard.sh` in dry-run/mock mode and verify all environment paths are correctly modified.
