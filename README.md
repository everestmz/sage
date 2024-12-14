# Sage

Integrate LLMS into any editor that supports LSP.

Sage started off as a project to add features like LLM support to [Helix](https://github.com/helix-editor/helix), and enable features like workspace symbol search in large codebases I was working with that crippled language servers like `pyright`.

It's since grown into an AI multitool for any editor that supports language servers.

## Features

### Smart context generation

Dynamically includes the most relevant symbols in the context window, using a combination of [tree-sitter](https://tree-sitter.github.io/tree-sitter/) and LSP clients. No need to manually `@mention` functions or types to include them in context!

### LLM completions

Uses [ollama](https://github.com/ollama/ollama) to power on-device code completion and LLM integrations.

![code action-based LLM completions](https://everestmz.github.io/assets/images/sage-demo.gif)

### Cursor support

Supports using LLMs hosted on Cursor's cloud (via [cursor-rpc](https://github.com/everestmz/cursor-rpc).

### Instant workspace symbol search

Sage can index a repository (using [llmcat](https://github.com/everestmz/llmcat)) to power global symbol search for language servers that don't support it (like `pylsp`), or for repos that are too large.

It can index a repository with >10M symbols (~20M LOC) in a few minutes, and searches through even gigabytes of symbols in milliseconds:

[![asciicast](https://asciinema.org/a/fhkTWEdRr7sqDgS5ZZtl5UUcQ.svg)](https://asciinema.org/a/fhkTWEdRr7sqDgS5ZZtl5UUcQ)
