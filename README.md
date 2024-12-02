# Sage

Integrate LLMS into any editor that supports LSP

## Features

### Instant workspace symbol search

Sage can index a repository (using [llmcat](https://github.com/everestmz/llmcat)) to power global symbol search for language servers that don't support it (like `pylsp`), or for repos that are too large.

It can index a repository with >10M symbols (~20M LOC) in a few minutes, and searches through even gigabytes of symbols in milliseconds:

[![asciicast](https://asciinema.org/a/fhkTWEdRr7sqDgS5ZZtl5UUcQ.svg)](https://asciinema.org/a/fhkTWEdRr7sqDgS5ZZtl5UUcQ)
