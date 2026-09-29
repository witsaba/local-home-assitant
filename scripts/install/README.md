# Witsaba Native Linux Installation Scripts

Modular installation scripts for deploying witsaba on Linux (Raspberry Pi, old PCs, etc.) without root privileges using Homebrew.

## Structure

```
scripts/install/
├── README.md                    # This file
├── 00-brew.sh                  # Check/install Homebrew
├── 01-postgresql.sh            # Install PostgreSQL
├── 02-go.sh                    # Install Go (for building binaries)
├── 03-node.sh                  # Install Node.js + pnpm
├── 04-postgres-init.sh        # Initialize PostgreSQL database
├── 10-build-go.sh              # Build Go services (messaging-core, workers)
├── 11-build-frontend.sh       # Build web_ui frontend
├── 12-systemd-services.sh     # Create systemd user services
└── main.sh                     # Main orchestrator
```

## Usage

```bash
# Run full installation (step by step)
./scripts/install/main.sh

# Or run individual steps
./scripts/install/00-brew.sh
./scripts/install/01-postgresql.sh
# etc.
```

## Requirements

- Linux (ARM64 or x86_64)
- Internet connection
- Homebrew (installed by 00-brew.sh if not present)
- No root privileges required (uses Homebrew in user space)

## Target: Raspberry Pi 1GB RAM

These scripts are optimized for:
- Ubuntu 24.04 ARM64
- 1GB RAM (memory-optimized configurations)
- User-space installation (no root)
