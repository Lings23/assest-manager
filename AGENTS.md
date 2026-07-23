# AGENTS.md

This file provides guidance to Codex (Codex.ai/code) when working with code in this repository.

## Project Overview

Asset Manager (资产信息管理系统) is a Go-based single-binary asset management system for managing 7 types of asset records. Designed for standalone deployment with embedded web frontend and SQLite database.

## Build & Run Commands

```bash
# Install dependencies
go mod tidy

# Run directly (development)
go run main.go

# Build executable
go build -o asset-manager          # Linux
go build -o asset-manager.exe      # Windows

# Cross-compile
GOOS=windows GOARCH=amd64 go build -o asset-manager.exe
GOOS=linux GOARCH=amd64 go build -o asset-manager

# Run with custom port
PORT=8081 go run main.go
```

**Default access**: http://localhost:8082
**Administrator bootstrap**: username `admin`; password comes from `ADMIN_PASSWORD` or is generated once and printed to the startup log

## Technology Stack

| Component | Technology | Notes |
|-----------|------------|-------|
| Language | Go 1.22+ | Single-binary deployment |
| Web Framework | Gin | HTTP routing and middleware |
| Database | SQLite (modernc.org/sqlite) | Pure Go, no CGO dependency |
| Auth | JWT + bcrypt | 24-hour token validity |
| Frontend | Embedded HTML + Chart.js | go:embed for single-file deployment |
| Export | encoding/csv | UTF-8 CSV export with Chinese headers |

## Project Structure

```
workspace/
├── main.go                      # Entry point, embeds web files
├── go.mod / go.sum              # Dependency management
├── internal/
│   ├── config/config.go         # Configuration (port, DB path, JWT secret)
│   ├── database/database.go     # SQLite init, table schemas, default admin
│   ├── models/models.go         # Data models (7 asset types + User + Log)
│   ├── handlers/
│   │   ├── auth.go              # Login/logout/current user
│   │   ├── assets_all.go        # Generic CRUD for all asset types
│   │   ├── import.go            # CSV/Excel import
│   │   └── stats.go             # Statistics endpoints
│   ├── middleware/auth.go       # JWT validation, AdminOnly middleware
│   ├── routes/routes.go         # Route registration
│   └── utils/
│       ├── jwt.go               # Token generation/validation
│       └── export.go            # Excel/CSV export utilities
├── web/index.html               # Embedded frontend (single file)
└── data/                        # Auto-created runtime data
    ├── assets.db                # SQLite database
    ├── backup/                  # Manual backups
    └── export/                  # Exported files
```

## Asset Types

The system manages 7 asset types through a unified generic handler:

| API Type Parameter | Model | Table |
|--------------------|-------|-------|
| `system-info` | SystemInfoAsset | system_info_assets |
| `hardware` | HardwareSoftwareAsset | hardware_software_assets |
| `data` | DataAsset | data_assets |
| `supply-chain` | SupplyChainAsset | supply_chain_assets |
| `vulnerability` | VulnerabilityAsset | vulnerability_assets |
| `software-stat` | SoftwareStatistics | software_statistics |
| `responsible-dept` | ResponsibleDepartment | responsible_departments |

All CRUD operations use the same route pattern: `/api/assets/:type`

## Key Architecture Patterns

### Generic Asset Handler

All asset types share a single handler in `assets_all.go`. The handler uses a type registry to map URL parameters to model structs and table names. When adding a new asset type:

1. Add model struct to `models/models.go`
2. Add table schema to `database/database.go`
3. Register in the type registry in `assets_all.go` (getAssetTypeInfo function)

### Embedded Frontend

The `web/index.html` is embedded via `//go:embed web/*`. After modifying frontend code, rebuild the executable to include changes.

### Database Initialization

Tables are created on first run via `createTables()` in `database/database.go`. The default admin user is created if no users exist.

### Data Directory

All runtime data (database, backups, exports) is stored in `data/` relative to the working directory. This directory is auto-created on startup.

## Authentication Flow

1. POST `/api/auth/login` with username/password → JWT token
2. Include token in `Authorization: Bearer <token>` header for protected routes
3. Middleware validates token and extracts user info
4. AdminOnly middleware blocks non-admin users for delete operations

## Role System

| Role | Permissions |
|------|-------------|
| admin | Full access, user management, delete assets, system settings |
| reporter | Create/edit own records, view all, export, statistics |

Reporters can only edit records they created (checked via `created_by` field).

## API Endpoints

### Authentication
- POST `/api/auth/login` - Login
- POST `/api/auth/logout` - Logout (token invalidation)
- GET `/api/auth/me` - Current user info

### Assets (all types)
- GET `/api/assets/:type` - List (pagination, search)
- POST `/api/assets/:type` - Create
- GET `/api/assets/:type/:id` - Get single
- PUT `/api/assets/:type/:id` - Update
- DELETE `/api/assets/:type/:id` - Delete (admin only)
- GET `/api/assets/:type/export` - Export Excel
- POST `/api/assets/:type/import` - Import CSV/Excel
- GET `/api/assets/:type/template` - Download import template

### Statistics
- GET `/api/stats/summary` - Overview counts
- GET `/api/stats/system-info` - System info stats
- GET `/api/stats/hardware` - Hardware stats
- GET `/api/stats/vulnerability` - Vulnerability stats

## Configuration

Environment variables (see `config/config.go`):
- `PORT` - Server port (default: 8082)
- `JWT_SECRET` - HS256 secret, at least 32 random characters in formal environments
- `ADMIN_PASSWORD` - bootstrap administrator password, at least 12 characters
- `JWT_TTL_HOURS` - access-token lifetime, default 2 hours

## Common Pitfalls

| Issue | Cause | Fix |
|-------|-------|-----|
| Module not found | Dependencies not downloaded | `go mod tidy` |
| Port 8080 occupied | Another service on port | Set `PORT=8081` |
| Frontend changes not reflected | Embedded files not rebuilt | `go build` again |
| Database locked | Concurrent write conflict | SQLite WAL mode handles this |
| Import fails | Wrong column format | Check template with `/api/assets/:type/template` |

## Import/Export Format

Import expects CSV with Chinese headers matching field descriptions. Use the template endpoint to get correct format:
```
GET /api/assets/:type/template
```

Export produces UTF-8 CSV files with Chinese headers.

## Soft Delete

All asset records use soft delete (`is_deleted` flag). Queries automatically filter deleted records. Admin can see deleted records by adding `?include_deleted=true` parameter.

## Date Field Format

Dates are stored as strings in `YYYY-MM-DD` format (VARCHAR(20) in SQLite). Models use `string` type for date fields, not `time.Time`.
