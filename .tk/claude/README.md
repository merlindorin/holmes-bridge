# Taskfile Claude Code Instructions

> This powerpack provides tasks to generate Claude Code instruction files for your project.

<!-- TOC -->
* [Taskfile Claude Code Instructions](#taskfile-claude-code-instructions)
  * [Summary](#summary)
  * [Prerequisites](#prerequisites)
  * [Configuration](#configuration)
  * [Usage](#usage)
    * [Example Integration](#example-integration)
    * [Generating Claude Instructions](#generating-claude-instructions)
    * [Default Behavior](#default-behavior)
  * [How It Works](#how-it-works)
<!-- TOC -->

## Summary

This Taskfile provides tasks for generating Claude Code instruction files in your project. It creates:

- **CLAUDE.md**: A convenience file for Claude Code users that always contains `@AGENT.md`
- **AGENT.md**: The actual instructions file containing project-specific guidelines, architecture, and conventions

The `@AGENT.md` directive in CLAUDE.md tells Claude Code to include the contents of AGENT.md. This allows projects to maintain a standard AGENT.md file while providing compatibility with Claude Code's CLAUDE.md convention.

## Prerequisites

* [Task](https://taskfile.dev/): A task runner / simpler Make alternative written in Go.

## Configuration

The Taskfile utilizes the following variables for customizing AGENT.md:

* `DEFAULT_AGENT_MD_FILE`: Output filename for AGENT.md (default: `AGENT.md`)
* `DEFAULT_AGENT_MD_CONTENT`: Template content for AGENT.md with placeholders for project-specific instructions

CLAUDE.md is not customizable - it always contains exactly `@AGENT.md`.

## Usage

The primary tasks provided in this Taskfile are:

- `agent`: Generates AGENT.md and CLAUDE.md files (CLAUDE.md can be skipped with SKIP_CLAUDE_MD=true)
- `default`: Runs the agent task

### Example Integration

Include this powerpack in your project's Taskfile.yaml:

```yaml
version: '3'

includes:
  claude: ./.tk/claude/Taskfile.yaml

tasks:
  default:
    cmds:
      - task: claude:default
```

### Generating Claude Instructions

To generate both CLAUDE.md and AGENT.md files:

```shell
task claude:agent
```

Or use the default task:

```shell
task claude:default
```

To skip generating CLAUDE.md:

```shell
SKIP_CLAUDE_MD=true task claude:agent
```

To customize the AGENT.md location:

```shell
AGENT_MD_FILE=.claude/AGENT.md task claude:agent
```

### Default Behavior

1. **AGENT.md** is created with a template containing sections for:
   - Project overview
   - Architecture documentation
   - Development workflow
   - Code standards
   - Testing requirements

2. **CLAUDE.md** is created with a single line: `@AGENT.md` (unless SKIP_CLAUDE_MD=true)

The task uses the `status` directive, so it will only create AGENT.md if it doesn't already exist. This prevents overwriting your customized instructions.

## How It Works

When Claude Code encounters a CLAUDE.md file, it reads it for instructions. The `@AGENT.md` directive tells Claude Code to include the contents of the AGENT.md file.

**Flow:**
1. User runs `task claude:agent` (or `task claude:default`)
2. AGENT.md is created with a template
3. CLAUDE.md is created with `@AGENT.md`
4. User customizes AGENT.md with project-specific instructions
5. When Claude Code runs, it reads CLAUDE.md, sees `@AGENT.md`, and includes AGENT.md

This pattern allows projects to use the standard AGENT.md convention while maintaining compatibility with Claude Code.