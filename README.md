# Witsaba Home Assistant System

This repository houses the complete codebase for the custom Home Assistant system.

## Running the Stack

**Linux (native):**

```bash
cp env.example .env  # edit passwords
docker compose up
```

**Mac (Docker Desktop):**

```bash
cp env.example .env  # edit passwords
docker compose -f docker-compose.mac.yml up
```

> **Note:** On Mac, device discovery (LAN scanning) does not work because Docker Desktop
> runs a Linux VM internally. For full LAN discovery, run the Linux compose file on a
> real Linux host.